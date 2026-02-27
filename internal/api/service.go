package api

// This file contains the gRPC service definitions.
// In a full build these would be generated from proto/api/v1/service.proto.
// For the MVP we define them manually to avoid requiring protoc.
// A JSON codec (codec.go) is registered so plain Go structs work as messages.

import (
	"context"

	"github.com/tunnelwhisperer/tw/internal/ops"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ── Request / Response types ────────────────────────────────────────────────

type Empty struct{}

type StatusResponse struct {
	Mode      string             `json:"mode"`
	Version   string             `json:"version"`
	Relay     ops.RelayStatus    `json:"relay"`
	UserCount int                `json:"user_count"`
	Server    *ops.ServerStatus  `json:"server,omitempty"`
	Client    *ops.ClientStatus  `json:"client,omitempty"`
}

type ConfigResponse struct {
	Config interface{} `json:"config"`
}

type SetModeRequest struct {
	Mode string `json:"mode"`
}

type ListProvidersResponse struct {
	Providers interface{} `json:"providers"`
}

type RelayStatusResponse struct {
	Relay interface{} `json:"relay"`
}

type TestCredentialsRequest struct {
	ProviderName string `json:"provider_name"`
	Token        string `json:"token"`
	AWSSecretKey string `json:"aws_secret_key"`
}

type ProvisionRelayRequest struct {
	Domain       string `json:"domain"`
	ProviderKey  string `json:"provider_key"`
	ProviderName string `json:"provider_name"`
	Token        string `json:"token"`
	AWSSecretKey string `json:"aws_secret_key"`
}

type ProvisionRelayResponse struct {
	Message string `json:"message"`
}

type DestroyRelayRequest struct {
	Creds map[string]string `json:"creds"`
}

type TestRelayResponse struct {
	Message string             `json:"message"`
	Steps   []TestRelayResult  `json:"steps,omitempty"`
}

type TestRelayResult struct {
	Label   string `json:"label"`
	Status  string `json:"status"` // "completed" or "failed"
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
}

type ListUsersResponse struct {
	Users []ops.UserInfo `json:"users"`
}

type CreateUserRequest struct {
	Name     string `json:"name"`
	Mappings []struct {
		ClientPort int `json:"client_port"`
		ServerPort int `json:"server_port"`
	} `json:"mappings"`
}

type DeleteUserRequest struct {
	Name string `json:"name"`
}

type GetUserConfigRequest struct {
	Name string `json:"name"`
}

type UserConfigResponse struct {
	Data []byte `json:"data"`
}

type UploadClientConfigRequest struct {
	Data []byte `json:"data"`
}

// ── Service interface ───────────────────────────────────────────────────────

type TunnelWhispererServer interface {
	GetStatus(ctx context.Context, req *Empty) (*StatusResponse, error)
	GetConfig(ctx context.Context, req *Empty) (*ConfigResponse, error)
	SetMode(ctx context.Context, req *SetModeRequest) (*Empty, error)
	ListProviders(ctx context.Context, req *Empty) (*ListProvidersResponse, error)
	GetRelayStatus(ctx context.Context, req *Empty) (*RelayStatusResponse, error)
	TestCredentials(ctx context.Context, req *TestCredentialsRequest) (*Empty, error)
	ProvisionRelay(ctx context.Context, req *ProvisionRelayRequest) (*ProvisionRelayResponse, error)
	DestroyRelay(ctx context.Context, req *DestroyRelayRequest) (*Empty, error)
	TestRelay(ctx context.Context, req *Empty) (*TestRelayResponse, error)
	StartServer(ctx context.Context, req *Empty) (*Empty, error)
	StopServer(ctx context.Context, req *Empty) (*Empty, error)
	StartClient(ctx context.Context, req *Empty) (*Empty, error)
	StopClient(ctx context.Context, req *Empty) (*Empty, error)
	UploadClientConfig(ctx context.Context, req *UploadClientConfigRequest) (*Empty, error)
	ListUsers(ctx context.Context, req *Empty) (*ListUsersResponse, error)
	CreateUser(ctx context.Context, req *CreateUserRequest) (*Empty, error)
	DeleteUser(ctx context.Context, req *DeleteUserRequest) (*Empty, error)
	GetUserConfig(ctx context.Context, req *GetUserConfigRequest) (*UserConfigResponse, error)
}

// ── Registration ────────────────────────────────────────────────────────────

func RegisterTunnelWhispererServer(s *grpc.Server, srv TunnelWhispererServer) {
	methods := []grpc.MethodDesc{
		unaryMethod("GetStatus", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(Empty)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).GetStatus(ctx, req)
		}),
		unaryMethod("GetConfig", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(Empty)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).GetConfig(ctx, req)
		}),
		unaryMethod("SetMode", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(SetModeRequest)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).SetMode(ctx, req)
		}),
		unaryMethod("ListProviders", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(Empty)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).ListProviders(ctx, req)
		}),
		unaryMethod("GetRelayStatus", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(Empty)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).GetRelayStatus(ctx, req)
		}),
		unaryMethod("TestCredentials", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(TestCredentialsRequest)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).TestCredentials(ctx, req)
		}),
		unaryMethod("ProvisionRelay", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(ProvisionRelayRequest)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).ProvisionRelay(ctx, req)
		}),
		unaryMethod("DestroyRelay", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(DestroyRelayRequest)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).DestroyRelay(ctx, req)
		}),
		unaryMethod("TestRelay", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(Empty)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).TestRelay(ctx, req)
		}),
		unaryMethod("StartServer", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(Empty)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).StartServer(ctx, req)
		}),
		unaryMethod("StopServer", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(Empty)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).StopServer(ctx, req)
		}),
		unaryMethod("StartClient", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(Empty)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).StartClient(ctx, req)
		}),
		unaryMethod("StopClient", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(Empty)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).StopClient(ctx, req)
		}),
		unaryMethod("UploadClientConfig", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(UploadClientConfigRequest)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).UploadClientConfig(ctx, req)
		}),
		unaryMethod("ListUsers", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(Empty)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).ListUsers(ctx, req)
		}),
		unaryMethod("CreateUser", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(CreateUserRequest)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).CreateUser(ctx, req)
		}),
		unaryMethod("DeleteUser", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(DeleteUserRequest)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).DeleteUser(ctx, req)
		}),
		unaryMethod("GetUserConfig", func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error) {
			req := new(GetUserConfigRequest)
			if err := dec(req); err != nil {
				return nil, err
			}
			return srv.(TunnelWhispererServer).GetUserConfig(ctx, req)
		}),
	}

	sd := grpc.ServiceDesc{
		ServiceName: "api.v1.TunnelWhisperer",
		HandlerType: (*TunnelWhispererServer)(nil),
		Methods:     methods,
		Streams:     []grpc.StreamDesc{},
	}
	s.RegisterService(&sd, srv)
}

// unaryMethod builds a grpc.MethodDesc with interceptor support.
func unaryMethod(name string, fn func(srv interface{}, ctx context.Context, dec func(interface{}) error) (interface{}, error)) grpc.MethodDesc {
	return grpc.MethodDesc{
		MethodName: name,
		Handler: func(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
			if interceptor == nil {
				return fn(srv, ctx, dec)
			}
			info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/api.v1.TunnelWhisperer/" + name}
			return interceptor(ctx, nil, info, func(ctx context.Context, _ interface{}) (interface{}, error) {
				return fn(srv, ctx, dec)
			})
		},
	}
}

// ── Unimplemented base ──────────────────────────────────────────────────────

type UnimplementedTunnelWhispererServer struct{}

func (UnimplementedTunnelWhispererServer) GetStatus(context.Context, *Empty) (*StatusResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) GetConfig(context.Context, *Empty) (*ConfigResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) SetMode(context.Context, *SetModeRequest) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) ListProviders(context.Context, *Empty) (*ListProvidersResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) GetRelayStatus(context.Context, *Empty) (*RelayStatusResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) TestCredentials(context.Context, *TestCredentialsRequest) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) ProvisionRelay(context.Context, *ProvisionRelayRequest) (*ProvisionRelayResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) DestroyRelay(context.Context, *DestroyRelayRequest) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) TestRelay(context.Context, *Empty) (*TestRelayResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) StartServer(context.Context, *Empty) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) StopServer(context.Context, *Empty) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) StartClient(context.Context, *Empty) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) StopClient(context.Context, *Empty) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) UploadClientConfig(context.Context, *UploadClientConfigRequest) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) ListUsers(context.Context, *Empty) (*ListUsersResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) CreateUser(context.Context, *CreateUserRequest) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) DeleteUser(context.Context, *DeleteUserRequest) (*Empty, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
func (UnimplementedTunnelWhispererServer) GetUserConfig(context.Context, *GetUserConfigRequest) (*UserConfigResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "not implemented")
}
