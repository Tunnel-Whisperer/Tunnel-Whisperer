package api

import (
	"log/slog"
	"net"

	"github.com/tunnelwhisperer/tw/internal/ops"
	"google.golang.org/grpc"
)

// Server wraps a gRPC server and the ops layer.
type Server struct {
	ops  *ops.Ops
	addr string
	gs   *grpc.Server
}

func NewServer(o *ops.Ops, addr string) *Server {
	gs := grpc.NewServer()
	s := &Server{
		ops:  o,
		addr: addr,
		gs:   gs,
	}
	RegisterTunnelWhispererServer(gs, &handler{ops: o})
	return s
}

// Run starts the gRPC server (blocking).
func (s *Server) Run() error {
	lis, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	slog.Info("gRPC server listening", "addr", s.addr)
	return s.gs.Serve(lis)
}

// Stop gracefully stops the gRPC server.
func (s *Server) Stop() {
	s.gs.GracefulStop()
}
