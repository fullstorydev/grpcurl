package main

//go:generate protoc --go_out=. --go-grpc_out=. bank.proto support.proto

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/status"
)

func main() {
	grpclog.SetLoggerV2(grpclog.NewLoggerV2(os.Stdout, os.Stdout, os.Stderr))

	port := flag.Int("port", 12345, "The port on which bankdemo gRPC server will listen.")
	datafile := flag.String("datafile", "accounts.json", "The path and filename to which bank account data is saved and from which data will be loaded.")
	flag.Parse()

	// create the server and load initial dataset
	ctx, cancel := context.WithCancel(context.Background())
	s := &svr{
		datafile: *datafile,
		ctx:      ctx,
		cancel:   cancel,
	}
	if err := s.load(); err != nil {
		panic(err)
	}

	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		panic(err)
	}

	grpcSvr := gRPCServer()

	// Register gRPC service implementations
	bankSvc := bankServer{
		allAccounts: &s.allAccounts,
	}
	RegisterBankServer(grpcSvr, &bankSvc)

	chatSvc := chatServer{
		chatsBySession: map[string]*session{},
	}
	RegisterSupportServer(grpcSvr, &chatSvc)

	go s.bgSaver()

	// don't forget to include server reflection support!
	reflection.Register(grpcSvr)

	defer func() {
		cancel()
		s.flush()
	}()

	// trap SIGINT / SIGTERM to exit cleanly
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGINT)
	signal.Notify(c, syscall.SIGTERM)
	go func() {
		<-c
		fmt.Println("Shutting down...")
		grpcSvr.GracefulStop()
	}()

	grpclog.Infof("server starting, listening on %v", l.Addr())
	if err := grpcSvr.Serve(l); err != nil {
		panic(err)
	}
}

func gRPCServer() *grpc.Server {
	var reqCounter uint64
	return grpc.NewServer(
		grpc.UnaryInterceptor(func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
			reqID := atomic.AddUint64(&reqCounter, 1)
			var client string
			if p, ok := peer.FromContext(ctx); ok {
				client = p.Addr.String()
			} else {
				client = "?"
			}
			grpclog.Infof("request %d started for %s from %s", reqID, info.FullMethod, client)

			rsp, err := handler(ctx, req)

			stat, _ := status.FromError(err)
			grpclog.Infof("request %d completed for %s from %s: %v %s", reqID, info.FullMethod, client, stat.Code(), stat.Message())
			return rsp, err

		}),
		grpc.StreamInterceptor(func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			reqID := atomic.AddUint64(&reqCounter, 1)
			var client string
			if p, ok := peer.FromContext(ss.Context()); ok {
				client = p.Addr.String()
			} else {
				client = "?"
			}
			grpclog.Infof("request %d started for %s from %s", reqID, info.FullMethod, client)

			err := handler(srv, ss)

			stat, _ := status.FromError(err)
			grpclog.Infof("request %d completed for %s from %s: %v %s", reqID, info.FullMethod, client, stat.Code(), stat.Message())
			return err
		}))
}

type svr struct {
	datafile    string
	ctx         context.Context
	cancel      context.CancelFunc
	allAccounts accounts
}

func (s *svr) load() error {
	accts, err := os.ReadFile(s.datafile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(accts) == 0 {
		s.allAccounts.AccountNumbersByCustomer = map[string][]uint64{}
		s.allAccounts.AccountsByNumber = map[uint64]*account{}
	} else if err := json.Unmarshal(accts, &s.allAccounts); err != nil {
		return err
	}

	return nil
}

func (s *svr) bgSaver() {
	ticker := time.NewTicker(5 * time.Second)
	for {
		select {
		case <-ticker.C:
			s.flush()
		case <-s.ctx.Done():
			ticker.Stop()
			return
		}
	}
}

func (s *svr) flush() {
	accounts := s.allAccounts.clone()

	if b, err := json.Marshal(accounts); err != nil {
		grpclog.Errorf("failed to save data to %q", s.datafile)
	} else if err := os.WriteFile(s.datafile, b, 0666); err != nil {
		grpclog.Errorf("failed to save data to %q", s.datafile)
	}
}
