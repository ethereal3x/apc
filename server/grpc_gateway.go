package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/ethereal3x/apc/config"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.uber.org/zap"
)

type HttpServer struct {
	Server
	serveMuxOptions  []runtime.ServeMuxOption
	registerFunction func(context.Context, *runtime.ServeMux) error
}

func (s *HttpServer) SetRegisterFunc(registerFunc func(context.Context, *runtime.ServeMux) error) {
	s.registerFunction = registerFunc
}

func (s *HttpServer) SetServeMuxOpts(opts []runtime.ServeMuxOption) {
	s.serveMuxOptions = opts
}

func NewHttpServer() *HttpServer {
	return &HttpServer{
		Server:          NewServer(config.GetConf().Server.GatewayAddr),
		serveMuxOptions: []runtime.ServeMuxOption{},
	}
}

// run 初始化 HTTP gateway mux 并注册服务路由
func (s *HttpServer) run(ctx context.Context) {
	mux := runtime.NewServeMux(
		append(s.serveMuxOptions, propagateTracingMetadata(), generateFrameworkMetadata(), setMarshalerOption())...)
	if s.registerFunction != nil {
		if err := s.registerFunction(ctx, mux); err != nil {
			s.log.Fatal("register function failed", zap.Error(err))
		}
	}

	hs := &http.Server{
		Addr:              s.address,
		Handler:           recovery(allowCORS(propagateTracing(mux))),
		ReadHeaderTimeout: 15 * time.Second,
		WriteTimeout:      15 * time.Second,
	}

	go func() {
		<-hsServiceQuit
		s.log.Info("gateway.Shutdown now")
		_ = hs.Shutdown(ctx)
		serviceWaitGroup.Done()
	}()

	if err := hs.ListenAndServe(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			return
		}
		s.log.Fatal("gateway.http.server.failed", zap.Error(err))
	}
}
