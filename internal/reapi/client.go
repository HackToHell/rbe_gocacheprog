package reapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

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
}

// NewClient creates a new REAPI client, connects, and discovers capabilities.
func NewClient(ctx context.Context, cfg ClientConfig) (*Client, error) {
	opts, err := dialOptions(cfg)
	if err != nil {
		return nil, err
	}

	dialCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()

	conn, err := grpc.DialContext(dialCtx, cfg.Target, opts...)
	if err != nil {
		return nil, fmt.Errorf("grpc dial %s: %w", cfg.Target, err)
	}

	c := &Client{
		conn:           conn,
		instanceName:   cfg.InstanceName,
		requestTimeout: cfg.RequestTimeout,
		maxBatchSize:   4 * 1024 * 1024, // default 4 MiB
		updateEnabled:  true,
	}

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

func dialOptions(cfg ClientConfig) ([]grpc.DialOption, error) {
	if cfg.TLSCert == "" && cfg.TLSKey == "" && cfg.TLSCA == "" {
		return []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, nil
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

	return []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg))}, nil
}
