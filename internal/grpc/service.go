package grpc

import (
	"context"

	"github.com/ronappleton/orchestrator/internal/engine"
	orchestratorpb "github.com/ronappleton/orchestrator/pkg/pb"
)

type OrchestratorService struct {
	orchestratorpb.UnimplementedOrchestratorServiceServer
	engine *engine.Service
}

func NewOrchestratorService(engineSvc *engine.Service) *OrchestratorService {
	return &OrchestratorService{engine: engineSvc}
}

func (s *OrchestratorService) StartSession(ctx context.Context, req *orchestratorpb.StartSessionRequest) (*orchestratorpb.StartSessionResponse, error) {
	sessionID, err := s.engine.StartSession(ctx, req)
	if err != nil {
		return nil, err
	}
	return &orchestratorpb.StartSessionResponse{SessionId: sessionID}, nil
}

func (s *OrchestratorService) StreamSessionEvents(req *orchestratorpb.StreamSessionEventsRequest, stream orchestratorpb.OrchestratorService_StreamSessionEventsServer) error {
	history, live, cancel, err := s.engine.Subscribe(req.GetSessionId())
	if err != nil {
		return err
	}
	defer cancel()

	for _, ev := range history {
		if err := stream.Send(ev); err != nil {
			return err
		}
	}

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case ev, ok := <-live:
			if !ok {
				return nil
			}
			if err := stream.Send(ev); err != nil {
				return err
			}
		}
	}
}

func (s *OrchestratorService) ResumeWithApproval(ctx context.Context, req *orchestratorpb.ResumeWithApprovalRequest) (*orchestratorpb.ResumeWithApprovalResponse, error) {
	return s.engine.ResumeWithApproval(ctx, req)
}
