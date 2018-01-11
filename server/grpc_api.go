package server

import (
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"time"

	context "golang.org/x/net/context"

	"github.com/absolute8511/ZanRedisDB/common"
	"github.com/absolute8511/ZanRedisDB/syncerpb"
	"google.golang.org/grpc"
)

var (
	proposeTimeout = time.Second * 4
)

type raftMeta struct {
	term  uint64
	index uint64
}
type grpcServerInfo struct {
	lastRaft map[string]raftMeta
}

func (s *Server) ApplyRaftReqs(ctx context.Context, reqs *syncerpb.RaftReqs) (*syncerpb.RpcErr, error) {
	if sLog.Level() >= common.LOG_DETAIL {
		sLog.Debugf("applying from remote cluster syncer: %v", reqs.String())
	}
	for _, r := range reqs.RaftLog {
		kv := s.GetNamespaceFromFullName(r.RaftGroupName)
		if kv == nil || !kv.IsReady() {
			return &syncerpb.RpcErr{ErrCode: http.StatusNotFound, ErrMsg: "namespace node not found"}, nil
		}
		if r.Type != syncerpb.EntryNormalRaw {
			// unsupported other type
		}

		// raft timestamp should be the same with the real raft request in data
		logStart := r.RaftTimestamp
		localStart := time.Now().UnixNano()
		var cost int64
		err := kv.Node.ProposeRawAndWait(r.Data, r.Term, r.Index, r.RaftTimestamp)
		if err != nil {
			sLog.Infof("propose failed: %v, err: %v", r.String(), err.Error())
			return &syncerpb.RpcErr{ErrCode: http.StatusInternalServerError, ErrMsg: err.Error()}, nil
		}
		cost = time.Now().UnixNano() - localStart
		if cost >= int64(proposeTimeout.Nanoseconds())/2 {
			sLog.Infof("slow for batch propose: %v, cost %v", r.RaftGroupName, cost)
		}
		// TODO: store log sync latency between clusters
		syncLatency := cost + localStart - logStart
		_ = syncLatency
	}
	return nil, nil
}

// serveHttpKVAPI starts a key-value server with a GET/PUT API and listens.
func (s *Server) serveGRPCAPI(port int, stopC <-chan struct{}) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	rpcServer := grpc.NewServer()
	syncerpb.RegisterCrossClusterAPIServer(rpcServer, s)
	go func() {
		<-stopC
		sLog.Infof("begin stopping grpc server")
		rpcServer.GracefulStop()
	}()
	err = rpcServer.Serve(lis)
	// exit when raft goes down
	sLog.Infof("grpc server stopped")
	if err != nil {
		return err
	}
	return nil
}