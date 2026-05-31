package server

import "testing"

func TestRunServer(t *testing.T) {
	rs := NewRpcServer()
	hs := NewHttpServer()
	rs.SetAddress(":8888")
	hs.SetAddress(":9999")
	RunGrpcGatewayService(rs, hs)
}
