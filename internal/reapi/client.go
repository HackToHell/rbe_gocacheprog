package reapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math/rand"
	"os"
	"time"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/genproto/googleapis/bytestream"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
)

const maxGRPCMessageSize = 64 * 1024 * 1024 // 64 MiB

// ClientConfig holds REAPI client configuration.
type ClientConfig struct {
	Target         string
	InstanceName   string
	TLSCert        string
	TLSKey         string
	TLSCA          string
	ConnectTimeout time.Duration
	RequestTimeout time.Duration
	MaxBatchSize   int64 // from capabilities, or default 4 MiB
}

// Client is the REAPI gRPC client.
type Client struct {
	conn           *grpc.ClientConn
	instanceName   string
	requestTimeout time.Duration
	maxBatchSize   int64
	updateEnabled  bool
	casClient      repb.ContentAddressableStorageClient
	acClient       repb.ActionCacheClient
	bsClient       bytestream.ByteStreamClient
	cb             *CircuitBreaker
}

// NewClient creates a new REAPI client, connects, and discovers capabilities.
func NewClient(ctx context.Context, cfg ClientConfig) (*Client, error) {
	opts, err := dialOptions(cfg)
	if err != nil {
		return nil, err
	}

	conn, err := grpc.NewClient(cfg.Target, opts...)
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", cfg.Target, err)
	}

	// grpc.NewClient connects lazily. Force a connection attempt within the
	// configured timeout so callers fail fast when the remote is unreachable.
	if cfg.ConnectTimeout > 0 {
		connectCtx, connectCancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
		defer connectCancel()
		conn.Connect()
		if !conn.WaitForStateChange(connectCtx, conn.GetState()) {
			// Context expired before any state change - remote is unreachable.
			conn.Close()
			return nil, fmt.Errorf("grpc connect %s: timed out after %s", cfg.Target, cfg.ConnectTimeout)
		}
	}

	c := &Client{
		conn:           conn,
		instanceName:   cfg.InstanceName,
		requestTimeout: cfg.RequestTimeout,
		maxBatchSize:   4 * 1024 * 1024, // default 4 MiB
		updateEnabled:  true,
	}

	c.casClient = repb.NewContentAddressableStorageClient(conn)
	c.acClient = repb.NewActionCacheClient(conn)
	c.bsClient = bytestream.NewByteStreamClient(conn)
	c.cb = NewCircuitBreaker(5, 30*time.Second)

	if err := c.discoverCapabilities(ctx); err != nil {
		// Non-fatal: use defaults and continue.
		// The caller may be in local-only mode.
		return c, nil
	}

	return c, nil
}

// NewClientFromConn creates a Client from an existing gRPC connection (for testing).
func NewClientFromConn(conn *grpc.ClientConn, instanceName string, requestTimeout time.Duration) *Client {
	return &Client{
		conn:           conn,
		instanceName:   instanceName,
		requestTimeout: requestTimeout,
		maxBatchSize:   4 * 1024 * 1024,
		updateEnabled:  true,
		casClient:      repb.NewContentAddressableStorageClient(conn),
		acClient:       repb.NewActionCacheClient(conn),
		bsClient:       bytestream.NewByteStreamClient(conn),
		cb:             NewCircuitBreaker(5, 30*time.Second),
	}
}

func (c *Client) discoverCapabilities(ctx context.Context) error {
	capClient := repb.NewCapabilitiesClient(c.conn)

	rCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	resp, err := capClient.GetCapabilities(rCtx, &repb.GetCapabilitiesRequest{
		InstanceName: c.instanceName,
	})
	if err != nil {
		return err
	}

	cc := resp.GetCacheCapabilities()
	if cc == nil {
		return nil
	}

	if cc.GetMaxBatchTotalSizeBytes() > 0 {
		c.maxBatchSize = cc.GetMaxBatchTotalSizeBytes()
	}
	if cc.GetActionCacheUpdateCapabilities() != nil {
		c.updateEnabled = cc.GetActionCacheUpdateCapabilities().GetUpdateEnabled()
	}

	return nil
}

// MaxBatchSize returns the server's max batch size.
func (c *Client) MaxBatchSize() int64 { return c.maxBatchSize }

// UpdateEnabled returns whether the server allows AC updates.
func (c *Client) UpdateEnabled() bool { return c.updateEnabled }

// Close closes the gRPC connection.
func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *Client) rpcCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.requestTimeout)
}

// CheckCircuit returns an error if the circuit breaker is open.
func (c *Client) CheckCircuit() error {
	if c.cb != nil && !c.cb.Allow() {
		return fmt.Errorf("circuit breaker open: remote temporarily unavailable")
	}
	return nil
}

// RecordSuccess records a successful RPC on the circuit breaker.
func (c *Client) RecordSuccess() {
	if c.cb != nil {
		c.cb.RecordSuccess()
	}
}

// RecordFailure records a failed RPC on the circuit breaker.
func (c *Client) RecordFailure() {
	if c.cb != nil {
		c.cb.RecordFailure()
	}
}

// retryRPCWithCtx retries an RPC function on transient errors with exponential backoff.
// retry attempt. If the parent context is already done, it returns immediately
// instead of wasting time on hopeless retries.
func retryRPCWithCtx[T any](ctx context.Context, cb *CircuitBreaker, fn func() (T, error)) (T, error) {
	const maxAttempts = 3
	const baseDelay = 100 * time.Millisecond

	var zero T
	if cb != nil && !cb.Allow() {
		return zero, fmt.Errorf("circuit breaker open: remote temporarily unavailable")
	}

	var lastErr error
	for attempt := range maxAttempts {
		if ctx.Err() != nil {
			return zero, ctx.Err()
		}
		result, err := fn()
		if err == nil {
			if cb != nil {
				cb.RecordSuccess()
			}
			return result, nil
		}
		code := status.Code(err)
		if code != codes.Unavailable && code != codes.ResourceExhausted && code != codes.DeadlineExceeded {
			return zero, err
		}
		lastErr = err
		if attempt < maxAttempts-1 {
			delay := baseDelay * time.Duration(1<<uint(attempt))
			jitter := time.Duration(rand.Int63n(int64(delay) / 2))
			time.Sleep(delay + jitter)
		}
	}
	if cb != nil {
		cb.RecordFailure()
	}
	return zero, lastErr
}

// retryRPCNoResultWithCtx is like retryRPCWithCtx but for functions that only
// return an error. It has its own loop to avoid the closure + generic wrapper
// overhead of delegating to retryRPCWithCtx (measurable in the upload hot path).
func retryRPCNoResultWithCtx(ctx context.Context, cb *CircuitBreaker, fn func() error) error {
	const maxAttempts = 3
	const baseDelay = 100 * time.Millisecond

	if cb != nil && !cb.Allow() {
		return fmt.Errorf("circuit breaker open: remote temporarily unavailable")
	}

	var lastErr error
	for attempt := range maxAttempts {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := fn()
		if err == nil {
			if cb != nil {
				cb.RecordSuccess()
			}
			return nil
		}
		code := status.Code(err)
		if code != codes.Unavailable && code != codes.ResourceExhausted && code != codes.DeadlineExceeded {
			return err
		}
		lastErr = err
		if attempt < maxAttempts-1 {
			delay := baseDelay * time.Duration(1<<uint(attempt))
			jitter := time.Duration(rand.Int63n(int64(delay) / 2))
			time.Sleep(delay + jitter)
		}
	}
	if cb != nil {
		cb.RecordFailure()
	}
	return lastErr
}

func dialOptions(cfg ClientConfig) ([]grpc.DialOption, error) {
	opts := []grpc.DialOption{
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(maxGRPCMessageSize),
			grpc.MaxCallSendMsgSize(maxGRPCMessageSize),
		),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	if cfg.TLSCert == "" && cfg.TLSKey == "" && cfg.TLSCA == "" {
		return append(opts, grpc.WithTransportCredentials(insecure.NewCredentials())), nil
	}

	tlsCfg := &tls.Config{}

	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return nil, fmt.Errorf("load TLS keypair: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	if cfg.TLSCA != "" {
		caCert, err := os.ReadFile(cfg.TLSCA)
		if err != nil {
			return nil, fmt.Errorf("read CA cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA cert")
		}
		tlsCfg.RootCAs = pool
	}

	return append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg))), nil
}
