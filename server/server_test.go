package server

import "testing"

func TestRunServer(t *testing.T) {
	rs := NewRpcServer()
	hs := NewHttpServer()
	rs.SetAddress(":8888")
	hs.SetAddress(":8888")
	RunGrpcGatewayService(rs, hs)
}
