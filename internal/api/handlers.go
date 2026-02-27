package api

import (
	"context"
	"log/slog"

	"github.com/tunnelwhisperer/tw/internal/ops"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type handler struct {
	UnimplementedTunnelWhispererServer
	ops *ops.Ops
}

// slogProgress returns a ProgressFunc that logs events via slog.
func slogProgress(e ops.ProgressEvent) {
	if e.Status == "failed" {
		slog.Error("progress", "step", e.Step, "label", e.Label, "error", e.Error)
	} else if e.Message != "" {
		slog.Info("progress", "step", e.Step, "label", e.Label, "status", e.Status, "msg", e.Message)
	}
}

func (h *handler) GetStatus(ctx context.Context, req *Empty) (*StatusResponse, error) {
	mode := h.ops.Mode()
	relay := h.ops.GetRelayStatus()
	users, _ := h.ops.ListUsers()

	resp := &StatusResponse{
		Mode:      mode,
		Version:   "0.1.0-dev",
		Relay:     relay,
		UserCount: len(users),
	}

	if mode == "server" {
		ss := h.ops.ServerStatus()
		resp.Server = &ss
	}
	if mode == "client" {
		cs := h.ops.ClientStatus()
		resp.Client = &cs
	}

	return resp, nil
}

func (h *handler) GetConfig(ctx context.Context, req *Empty) (*ConfigResponse, error) {
	return &ConfigResponse{Config: h.ops.Config()}, nil
}

func (h *handler) SetMode(ctx context.Context, req *SetModeRequest) (*Empty, error) {
	if err := h.ops.SetMode(req.Mode); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	return &Empty{}, nil
}

func (h *handler) ListProviders(ctx context.Context, req *Empty) (*ListProvidersResponse, error) {
	return &ListProvidersResponse{Providers: ops.CloudProviders()}, nil
}

func (h *handler) GetRelayStatus(ctx context.Context, req *Empty) (*RelayStatusResponse, error) {
	return &RelayStatusResponse{Relay: h.ops.GetRelayStatus()}, nil
}

func (h *handler) TestCredentials(ctx context.Context, req *TestCredentialsRequest) (*Empty, error) {
	if err := h.ops.TestCloudCredentials(req.ProviderName, req.Token, req.AWSSecretKey); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	return &Empty{}, nil
}

func (h *handler) ProvisionRelay(ctx context.Context, req *ProvisionRelayRequest) (*ProvisionRelayResponse, error) {
	opsReq := ops.RelayProvisionRequest{
		Domain:       req.Domain,
		ProviderKey:  req.ProviderKey,
		ProviderName: req.ProviderName,
		Token:        req.Token,
		AWSSecretKey: req.AWSSecretKey,
	}
	if err := h.ops.ProvisionRelay(ctx, opsReq, slogProgress); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &ProvisionRelayResponse{Message: "relay provisioned"}, nil
}

func (h *handler) DestroyRelay(ctx context.Context, req *DestroyRelayRequest) (*Empty, error) {
	if err := h.ops.DestroyRelay(ctx, req.Creds, slogProgress); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &Empty{}, nil
}

func (h *handler) TestRelay(ctx context.Context, req *Empty) (*TestRelayResponse, error) {
	var steps []TestRelayResult
	h.ops.TestRelay(func(e ops.ProgressEvent) {
		slogProgress(e)
		if e.Status == "completed" || e.Status == "failed" {
			steps = append(steps, TestRelayResult{
				Label:   e.Label,
				Status:  e.Status,
				Message: e.Message,
				Error:   e.Error,
			})
		}
	})
	return &TestRelayResponse{Message: "test complete", Steps: steps}, nil
}

func (h *handler) StartServer(ctx context.Context, req *Empty) (*Empty, error) {
	if err := h.ops.StartServer(slogProgress); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &Empty{}, nil
}

func (h *handler) StopServer(ctx context.Context, req *Empty) (*Empty, error) {
	if err := h.ops.StopServer(slogProgress); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &Empty{}, nil
}

func (h *handler) StartClient(ctx context.Context, req *Empty) (*Empty, error) {
	if err := h.ops.StartClient(slogProgress); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &Empty{}, nil
}

func (h *handler) StopClient(ctx context.Context, req *Empty) (*Empty, error) {
	if err := h.ops.StopClient(slogProgress); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &Empty{}, nil
}

func (h *handler) UploadClientConfig(ctx context.Context, req *UploadClientConfigRequest) (*Empty, error) {
	if err := h.ops.UploadClientConfig(req.Data); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	return &Empty{}, nil
}

func (h *handler) ListUsers(ctx context.Context, req *Empty) (*ListUsersResponse, error) {
	users, err := h.ops.ListUsers()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &ListUsersResponse{Users: users}, nil
}

func (h *handler) CreateUser(ctx context.Context, req *CreateUserRequest) (*Empty, error) {
	mappings := make([]ops.PortMapping, len(req.Mappings))
	for i, m := range req.Mappings {
		mappings[i] = ops.PortMapping{ClientPort: m.ClientPort, ServerPort: m.ServerPort}
	}
	opsReq := ops.CreateUserRequest{
		Name:     req.Name,
		Mappings: mappings,
	}
	if err := h.ops.CreateUser(ctx, opsReq, slogProgress); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &Empty{}, nil
}

func (h *handler) DeleteUser(ctx context.Context, req *DeleteUserRequest) (*Empty, error) {
	if err := h.ops.DeleteUser(req.Name); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &Empty{}, nil
}

func (h *handler) GetUserConfig(ctx context.Context, req *GetUserConfigRequest) (*UserConfigResponse, error) {
	data, err := h.ops.GetUserConfigBundle(req.Name)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "%v", err)
	}
	return &UserConfigResponse{Data: data}, nil
}
