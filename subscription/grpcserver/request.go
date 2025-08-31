package grpcserver

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"v2ray-stat/subscription/proto"
)

func SendRequest() (*proto.Response, error) {
    mu.Lock()
    if currentStream == nil {
        if lastResponse != nil {
            resp := lastResponse
            mu.Unlock()
            cfg.Logger.Warn("No gRPC connection, returning cached data")
            return resp, nil
        }
        mu.Unlock()
        return nil, fmt.Errorf("no gRPC connection and no cached data")
    }

    id := uuid.New().String()
    ch := make(chan *proto.Response, 1)
    waiting[id] = ch
    strm := currentStream
    mu.Unlock()

    cfg.Logger.Debug("Sending gRPC request", "request_id", id)
    if err := strm.Send(&proto.Request{RequestId: id}); err != nil {
        mu.Lock()
        delete(waiting, id)
        mu.Unlock()
        return nil, fmt.Errorf("failed to send gRPC request: %w", err)
    }

    select {
    case resp := <-ch:
        mu.Lock()
        lastResponse = resp
        mu.Unlock()
        cfg.Logger.Debug("Received gRPC response", "request_id", id)
        return resp, nil
    case <-time.After(10 * time.Second):
        mu.Lock()
        delete(waiting, id)
        if lastResponse != nil {
            resp := lastResponse
            mu.Unlock()
            cfg.Logger.Warn("gRPC timeout, returning cached data")
            return resp, nil
        }
        mu.Unlock()
        return nil, fmt.Errorf("gRPC timeout and no cached data")
    }
}
