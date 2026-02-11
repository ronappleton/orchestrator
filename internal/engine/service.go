package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	approvalpb "github.com/ronappleton/approval-service/pkg/pb"
	"github.com/ronappleton/orchestrator/internal/config"
	orchestratorpb "github.com/ronappleton/orchestrator/pkg/pb"
	policyv1 "github.com/ronappleton/policy-service/pkg/pb"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	gproto "google.golang.org/protobuf/proto"
)

type Service struct {
	cfg    config.Config
	logger *zap.Logger

	mu                 sync.RWMutex
	sessions           map[string]*sessionState
	challengeToSession map[string]string

	policyConn      *grpc.ClientConn
	policyClient    policyv1.PolicyServiceClient
	policyTimeout   time.Duration
	approvalConn    *grpc.ClientConn
	approvalClient  approvalpb.ApprovalServiceClient
	approvalTimeout time.Duration
}

type sessionState struct {
	SessionID   string
	Mode        string
	Prompt      string
	WorkspaceID string
	ProjectID   string
	UserID      string

	mu          sync.Mutex
	history     []*orchestratorpb.SessionEvent
	subscribers map[int]chan *orchestratorpb.SessionEvent
	nextSubID   int
	paused      *pausedStep
}

type pausedStep struct {
	ChallengeID string
	Check       *policyv1.CheckRequest
	Obligations []*policyv1.Obligation
}

func NewService(cfg config.Config, logger *zap.Logger) *Service {
	policyTimeout := parseDuration(cfg.Policy.Timeout, 5*time.Second)
	approvalTimeout := parseDuration(cfg.Approval.Timeout, 5*time.Second)
	return &Service{
		cfg:                cfg,
		logger:             logger,
		sessions:           map[string]*sessionState{},
		challengeToSession: map[string]string{},
		policyTimeout:      policyTimeout,
		approvalTimeout:    approvalTimeout,
	}
}

func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.policyConn != nil {
		_ = s.policyConn.Close()
		s.policyConn = nil
		s.policyClient = nil
	}
	if s.approvalConn != nil {
		_ = s.approvalConn.Close()
		s.approvalConn = nil
		s.approvalClient = nil
	}
}

func (s *Service) StartSession(ctx context.Context, req *orchestratorpb.StartSessionRequest) (string, error) {
	sessionID := uuid.NewString()
	state := &sessionState{
		SessionID:   sessionID,
		Mode:        strings.ToUpper(strings.TrimSpace(req.GetMode())),
		Prompt:      req.GetPrompt(),
		WorkspaceID: req.GetWorkspaceId(),
		ProjectID:   req.GetProjectId(),
		UserID:      req.GetUserId(),
		history:     make([]*orchestratorpb.SessionEvent, 0, 64),
		subscribers: map[int]chan *orchestratorpb.SessionEvent{},
	}
	if state.UserID == "" {
		state.UserID = "anonymous"
	}
	if state.Mode == "" {
		state.Mode = "REPAIR"
	}

	s.mu.Lock()
	s.sessions[sessionID] = state
	s.mu.Unlock()

	s.publishEvent(sessionID, "status", map[string]any{"status": "running"})
	go s.runInitialStep(sessionID)
	return sessionID, nil
}

func (s *Service) Subscribe(sessionID string) ([]*orchestratorpb.SessionEvent, <-chan *orchestratorpb.SessionEvent, func(), error) {
	state, err := s.getSession(sessionID)
	if err != nil {
		return nil, nil, nil, err
	}

	state.mu.Lock()
	history := make([]*orchestratorpb.SessionEvent, len(state.history))
	for i, ev := range state.history {
		cp := *ev
		history[i] = &cp
	}
	ch := make(chan *orchestratorpb.SessionEvent, 32)
	subID := state.nextSubID
	state.nextSubID++
	state.subscribers[subID] = ch
	state.mu.Unlock()

	cancel := func() {
		state.mu.Lock()
		if existing, ok := state.subscribers[subID]; ok {
			delete(state.subscribers, subID)
			close(existing)
		}
		state.mu.Unlock()
	}
	return history, ch, cancel, nil
}

func (s *Service) ResumeWithApproval(ctx context.Context, req *orchestratorpb.ResumeWithApprovalRequest) (*orchestratorpb.ResumeWithApprovalResponse, error) {
	if strings.TrimSpace(req.GetChallengeId()) == "" {
		return &orchestratorpb.ResumeWithApprovalResponse{Status: "ERROR", Message: "challenge_id is required"}, nil
	}
	if strings.TrimSpace(req.GetApprovalCapability()) == "" {
		return &orchestratorpb.ResumeWithApprovalResponse{Status: "ERROR", Message: "approval_capability is required"}, nil
	}

	sessionID := strings.TrimSpace(req.GetSessionId())
	if sessionID == "" {
		s.mu.RLock()
		sessionID = s.challengeToSession[req.GetChallengeId()]
		s.mu.RUnlock()
	}
	state, err := s.getSession(sessionID)
	if err != nil {
		return &orchestratorpb.ResumeWithApprovalResponse{Status: "ERROR", Message: "session not found"}, nil
	}

	state.mu.Lock()
	paused := state.paused
	state.mu.Unlock()
	if paused == nil || paused.ChallengeID != req.GetChallengeId() {
		return &orchestratorpb.ResumeWithApprovalResponse{Status: "ERROR", Message: "no paused workflow for challenge"}, nil
	}

	checkReq := gproto.Clone(paused.Check).(*policyv1.CheckRequest)
	checkReq.ApprovalCapability = req.GetApprovalCapability()

	policyResp, err := s.callPolicyCheck(ctx, checkReq)
	if err != nil {
		s.publishEvent(sessionID, "error", map[string]any{"message": err.Error()})
		return &orchestratorpb.ResumeWithApprovalResponse{Status: "ERROR", Message: err.Error()}, nil
	}

	switch policyResp.GetDecision() {
	case policyv1.Decision_ALLOW:
		if _, err := s.callMarkUsed(ctx, req.GetChallengeId(), req.GetTokenId()); err != nil {
			s.publishEvent(sessionID, "error", map[string]any{"message": fmt.Sprintf("mark used failed: %v", err)})
			return &orchestratorpb.ResumeWithApprovalResponse{Status: "ERROR", Message: err.Error()}, nil
		}
		if err := enforceObligations(policyResp.GetObligations(), checkReq); err != nil {
			s.publishEvent(sessionID, "error", map[string]any{"message": err.Error()})
			return &orchestratorpb.ResumeWithApprovalResponse{Status: "ERROR", Message: err.Error()}, nil
		}
		s.publishEvent(sessionID, "approval_resolved", map[string]any{"challenge_id": req.GetChallengeId(), "status": "approved"})
		s.executeAction(sessionID, checkReq)
		state.mu.Lock()
		state.paused = nil
		state.mu.Unlock()
		s.mu.Lock()
		delete(s.challengeToSession, req.GetChallengeId())
		s.mu.Unlock()
		return &orchestratorpb.ResumeWithApprovalResponse{Status: "OK", Message: "resumed"}, nil
	case policyv1.Decision_REQUIRE_APPROVAL:
		s.publishEvent(sessionID, "approval_required", map[string]any{
			"challenge_id": req.GetChallengeId(),
			"reason":       policyResp.GetReason(),
		})
		return &orchestratorpb.ResumeWithApprovalResponse{Status: "PENDING", Message: "approval still required"}, nil
	default:
		s.publishEvent(sessionID, "error", map[string]any{"message": policyResp.GetReason()})
		return &orchestratorpb.ResumeWithApprovalResponse{Status: "DENIED", Message: policyResp.GetReason()}, nil
	}
}

func (s *Service) runInitialStep(sessionID string) {
	state, err := s.getSession(sessionID)
	if err != nil {
		return
	}
	checkReq := buildCheckRequest(state)
	policyResp, err := s.callPolicyCheck(context.Background(), checkReq)
	if err != nil {
		s.publishEvent(sessionID, "error", map[string]any{"message": err.Error()})
		return
	}

	switch policyResp.GetDecision() {
	case policyv1.Decision_ALLOW:
		if err := enforceObligations(policyResp.GetObligations(), checkReq); err != nil {
			s.publishEvent(sessionID, "error", map[string]any{"message": err.Error()})
			return
		}
		s.executeAction(sessionID, checkReq)
	case policyv1.Decision_DENY:
		s.publishEvent(sessionID, "error", map[string]any{"message": policyResp.GetReason()})
	case policyv1.Decision_REQUIRE_APPROVAL:
		challengePayload := buildChallengePayload(checkReq, policyResp.GetObligations(), policyResp.GetApproval().GetFingerprintHash())
		payloadJSON, _ := json.Marshal(challengePayload)
		createResp, err := s.callCreateChallenge(context.Background(), &approvalpb.CreateChallengeRequest{
			WorkspaceId:        checkReq.GetContext().GetWorkspaceId(),
			RequestedForUserId: checkReq.GetSubject().GetActorId(),
			RequestedByService: "orchestrator",
			Scope:              toApprovalScope(policyResp.GetApproval().GetScope()),
			FingerprintHash:    policyResp.GetApproval().GetFingerprintHash(),
			Reason:             policyResp.GetApproval().GetReason(),
			Summary:            buildApprovalSummary(checkReq),
			PayloadJson:        string(payloadJSON),
			TtlSeconds:         policyResp.GetApproval().GetExpiresInSeconds(),
		})
		if err != nil {
			s.publishEvent(sessionID, "error", map[string]any{"message": fmt.Sprintf("create challenge failed: %v", err)})
			return
		}

		state.mu.Lock()
		state.paused = &pausedStep{
			ChallengeID: createResp.GetChallengeId(),
			Check:       checkReq,
			Obligations: policyResp.GetObligations(),
		}
		state.mu.Unlock()
		s.mu.Lock()
		s.challengeToSession[createResp.GetChallengeId()] = sessionID
		s.mu.Unlock()

		s.publishEvent(sessionID, "approval_required", map[string]any{
			"challenge_id":     createResp.GetChallengeId(),
			"reason":           policyResp.GetApproval().GetReason(),
			"scope":            policyResp.GetApproval().GetScope().String(),
			"fingerprint_hash": policyResp.GetApproval().GetFingerprintHash(),
			"expires_at_unix":  createResp.GetExpiresAtUnix(),
			"payload_json":     string(payloadJSON),
		})
	}
}

func (s *Service) executeAction(sessionID string, req *policyv1.CheckRequest) {
	s.publishEvent(sessionID, "progress", map[string]any{"phase": "execute", "action": req.GetAction()})
	if strings.HasPrefix(req.GetAction(), "media.") {
		s.publishEvent(sessionID, "artifact_created", map[string]any{"action": req.GetAction(), "project_id": req.GetContext().GetProjectId()})
	}
	s.publishEvent(sessionID, "completed", map[string]any{"status": "ok"})
}

func (s *Service) publishEvent(sessionID string, eventType string, payload map[string]any) {
	state, err := s.getSession(sessionID)
	if err != nil {
		return
	}
	blob, _ := json.Marshal(payload)
	ev := &orchestratorpb.SessionEvent{
		SessionId:        sessionID,
		EventType:        eventType,
		DataJson:         string(blob),
		ObservedAtUnixMs: time.Now().UnixMilli(),
	}

	state.mu.Lock()
	if len(state.history) >= 200 {
		state.history = append(state.history[1:], ev)
	} else {
		state.history = append(state.history, ev)
	}
	for id, ch := range state.subscribers {
		select {
		case ch <- ev:
		default:
			close(ch)
			delete(state.subscribers, id)
		}
	}
	state.mu.Unlock()
}

func (s *Service) getSession(sessionID string) (*sessionState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("session not found")
	}
	return state, nil
}

func (s *Service) callPolicyCheck(ctx context.Context, req *policyv1.CheckRequest) (*policyv1.CheckResponse, error) {
	client, err := s.policy(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.policyTimeout)
	defer cancel()
	return client.Check(callCtx, req)
}

func (s *Service) callCreateChallenge(ctx context.Context, req *approvalpb.CreateChallengeRequest) (*approvalpb.CreateChallengeResponse, error) {
	client, err := s.approval(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.approvalTimeout)
	defer cancel()
	return client.CreateChallenge(callCtx, req)
}

func (s *Service) callMarkUsed(ctx context.Context, challengeID string, tokenID string) (*approvalpb.MarkUsedResponse, error) {
	client, err := s.approval(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, s.approvalTimeout)
	defer cancel()
	return client.MarkUsed(callCtx, &approvalpb.MarkUsedRequest{ChallengeId: challengeID, TokenId: tokenID})
}

func (s *Service) policy(ctx context.Context) (policyv1.PolicyServiceClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.policyClient != nil {
		return s.policyClient, nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, s.policyTimeout)
	defer cancel()
	conn, err := grpc.DialContext(
		dialCtx,
		s.cfg.Policy.GRPCAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
	)
	if err != nil {
		return nil, err
	}
	s.policyConn = conn
	s.policyClient = policyv1.NewPolicyServiceClient(conn)
	return s.policyClient, nil
}

func (s *Service) approval(ctx context.Context) (approvalpb.ApprovalServiceClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.approvalClient != nil {
		return s.approvalClient, nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, s.approvalTimeout)
	defer cancel()
	conn, err := grpc.DialContext(
		dialCtx,
		s.cfg.Approval.GRPCAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
	)
	if err != nil {
		return nil, err
	}
	s.approvalConn = conn
	s.approvalClient = approvalpb.NewApprovalServiceClient(conn)
	return s.approvalClient, nil
}

func parseDuration(raw string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(raw) == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func buildCheckRequest(state *sessionState) *policyv1.CheckRequest {
	action := "scm.pr.open"
	tool := "scm"
	mode := policyv1.Mode_REPAIR
	phase := policyv1.Phase_IMPLEMENT

	switch strings.ToUpper(strings.TrimSpace(state.Mode)) {
	case "BUILD":
		mode = policyv1.Mode_BUILD
		action = "scm.commit.create"
	case "MEDIA":
		mode = policyv1.Mode_MEDIA
		action = "media.image.generate"
		tool = "media"
	case "OPS":
		mode = policyv1.Mode_OPS
		action = "command.exec"
		tool = "command"
	}

	return &policyv1.CheckRequest{
		Subject: &policyv1.Subject{
			Service:   "orchestrator",
			ActorType: policyv1.ActorType_USER,
			ActorId:   state.UserID,
		},
		Action: action,
		Context: &policyv1.Context{
			WorkspaceId:   state.WorkspaceID,
			ProjectId:     state.ProjectID,
			CorrelationId: state.SessionID,
			Mode:          mode,
			Phase:         phase,
			AttemptNumber: 1,
		},
		Resource: &policyv1.Resource{
			Repo: &policyv1.RepoRef{
				Provider: "local",
				Owner:    "ronappleton",
				Name:     "ai-eco-system",
				Ref:      "main",
			},
			Paths:      []string{"README.md"},
			Command:    state.Prompt,
			Tool:       tool,
			Model:      "",
			WorkflowId: "",
		},
		Diff: &policyv1.DiffMeta{
			FilesChanged: 1,
			LocAdded:     10,
			LocRemoved:   0,
			DepsChanged:  false,
			Deps:         []string{},
		},
	}
}

func buildApprovalSummary(check *policyv1.CheckRequest) string {
	repo := check.GetResource().GetRepo()
	paths := check.GetResource().GetPaths()
	if len(paths) == 0 {
		paths = []string{"(none)"}
	}
	return fmt.Sprintf("Approve %s in %s/%s touching %s", check.GetAction(), repo.GetOwner(), repo.GetName(), strings.Join(paths, ","))
}

func buildChallengePayload(check *policyv1.CheckRequest, obligations []*policyv1.Obligation, fingerprint string) map[string]any {
	obligationPayloads := make([]map[string]any, 0, len(obligations))
	for _, ob := range obligations {
		item := map[string]any{"type": ob.GetType().String()}
		switch params := ob.GetParams().(type) {
		case *policyv1.Obligation_MaxDiff:
			item["max_files"] = params.MaxDiff.GetMaxFiles()
			item["max_loc_add"] = params.MaxDiff.GetMaxLocAdd()
			item["max_loc_del"] = params.MaxDiff.GetMaxLocDel()
		case *policyv1.Obligation_Tests_:
			item["suite"] = params.Tests.GetSuite()
		case *policyv1.Obligation_Lint_:
			item["profile"] = params.Lint.GetProfile()
		}
		obligationPayloads = append(obligationPayloads, item)
	}
	return map[string]any{
		"action":       check.GetAction(),
		"mode":         check.GetContext().GetMode().String(),
		"phase":        check.GetContext().GetPhase().String(),
		"workspace_id": check.GetContext().GetWorkspaceId(),
		"project_id":   check.GetContext().GetProjectId(),
		"repo": map[string]any{
			"provider": check.GetResource().GetRepo().GetProvider(),
			"owner":    check.GetResource().GetRepo().GetOwner(),
			"name":     check.GetResource().GetRepo().GetName(),
			"ref":      check.GetResource().GetRepo().GetRef(),
		},
		"paths": check.GetResource().GetPaths(),
		"diff": map[string]any{
			"files_changed": check.GetDiff().GetFilesChanged(),
			"loc_added":     check.GetDiff().GetLocAdded(),
			"loc_removed":   check.GetDiff().GetLocRemoved(),
			"deps_changed":  check.GetDiff().GetDepsChanged(),
			"deps":          check.GetDiff().GetDeps(),
		},
		"obligations":      obligationPayloads,
		"fingerprint_hash": fingerprint,
	}
}

func toApprovalScope(scope policyv1.ApprovalScope) approvalpb.ApprovalScope {
	switch scope {
	case policyv1.ApprovalScope_APPROVAL_SCOPE_SPEC:
		return approvalpb.ApprovalScope_SPEC
	case policyv1.ApprovalScope_APPROVAL_SCOPE_PLAN:
		return approvalpb.ApprovalScope_PLAN
	case policyv1.ApprovalScope_APPROVAL_SCOPE_CHANGESET:
		return approvalpb.ApprovalScope_CHANGESET
	case policyv1.ApprovalScope_APPROVAL_SCOPE_PR:
		return approvalpb.ApprovalScope_PR
	case policyv1.ApprovalScope_APPROVAL_SCOPE_MEDIA_ASSET:
		return approvalpb.ApprovalScope_MEDIA_ASSET
	case policyv1.ApprovalScope_APPROVAL_SCOPE_OPERATION:
		return approvalpb.ApprovalScope_OPERATION
	default:
		return approvalpb.ApprovalScope_APPROVAL_SCOPE_UNSPECIFIED
	}
}

func enforceObligations(obligations []*policyv1.Obligation, req *policyv1.CheckRequest) error {
	for _, ob := range obligations {
		switch ob.GetType() {
		case policyv1.ObligationType_MAX_DIFF_BUDGET:
			max := ob.GetMaxDiff()
			diff := req.GetDiff()
			if diff.GetFilesChanged() > max.GetMaxFiles() || diff.GetLocAdded() > max.GetMaxLocAdd() || diff.GetLocRemoved() > max.GetMaxLocDel() {
				return fmt.Errorf("max diff budget exceeded")
			}
		case policyv1.ObligationType_NO_DEP_CHANGES:
			if req.GetDiff().GetDepsChanged() {
				return fmt.Errorf("dependency changes are not allowed")
			}
		case policyv1.ObligationType_MAX_ATTEMPTS:
			if req.GetContext().GetAttemptNumber() > ob.GetAttempts().GetMaxAttempts() {
				return fmt.Errorf("max attempts exceeded")
			}
		case policyv1.ObligationType_REQUIRE_EVIDENCE_LINKS:
			if req.GetEvidenceLinks() < ob.GetEvidence().GetMinLinks() {
				return fmt.Errorf("insufficient evidence links")
			}
		case policyv1.ObligationType_REQUIRE_CANONICAL_PACKS:
			if req.GetResource().GetTool() == "media" {
				if strings.TrimSpace(req.GetResource().GetModel()) == "" || strings.TrimSpace(req.GetResource().GetWorkflowId()) == "" {
					return fmt.Errorf("canonical packs are required for media workflows")
				}
			}
		}
	}
	return nil
}
