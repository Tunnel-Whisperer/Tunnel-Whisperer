package api

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Client is a gRPC client for the TunnelWhisperer API.
type Client struct {
	conn *grpc.ClientConn
}

// Dial connects to the gRPC API server at the given address.
// Returns an error if the server is not reachable within 2 seconds.
func Dial(addr string) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype("json")),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn}, nil
}

// Close closes the underlying connection.
func (c *Client) Close() {
	c.conn.Close()
}

func (c *Client) invoke(ctx context.Context, method string, req, resp interface{}) error {
	return c.conn.Invoke(ctx, "/api.v1.TunnelWhisperer/"+method, req, resp)
}

// GetStatus calls the GetStatus RPC.
func (c *Client) GetStatus(ctx context.Context) (*StatusResponse, error) {
	resp := &StatusResponse{}
	err := c.invoke(ctx, "GetStatus", &Empty{}, resp)
	return resp, err
}

// TestRelay calls the TestRelay RPC.
func (c *Client) TestRelay(ctx context.Context) (*TestRelayResponse, error) {
	resp := &TestRelayResponse{}
	err := c.invoke(ctx, "TestRelay", &Empty{}, resp)
	return resp, err
}

// ListUsers calls the ListUsers RPC.
func (c *Client) ListUsers(ctx context.Context) (*ListUsersResponse, error) {
	resp := &ListUsersResponse{}
	err := c.invoke(ctx, "ListUsers", &Empty{}, resp)
	return resp, err
}

// DeleteUser calls the DeleteUser RPC.
func (c *Client) DeleteUser(ctx context.Context, name string) error {
	return c.invoke(ctx, "DeleteUser", &DeleteUserRequest{Name: name}, &Empty{})
}

// DestroyRelay calls the DestroyRelay RPC.
func (c *Client) DestroyRelay(ctx context.Context, creds map[string]string) error {
	return c.invoke(ctx, "DestroyRelay", &DestroyRelayRequest{Creds: creds}, &Empty{})
}

// GetUserConfig calls the GetUserConfig RPC and returns the zip bundle.
func (c *Client) GetUserConfig(ctx context.Context, name string) ([]byte, error) {
	resp := &UserConfigResponse{}
	err := c.invoke(ctx, "GetUserConfig", &GetUserConfigRequest{Name: name}, resp)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}
