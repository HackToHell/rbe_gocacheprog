package reapi

import (
	"context"
	"fmt"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetActionResult looks up an ActionResult by action digest.
// Returns (nil, nil) on cache miss (NOT_FOUND).
func (c *Client) GetActionResult(ctx context.Context, actionDigest Digest) (*repb.ActionResult, error) {
	rCtx, cancel := c.rpcCtx(ctx)
	defer cancel()

	acClient := repb.NewActionCacheClient(c.conn)
	resp, err := acClient.GetActionResult(rCtx, &repb.GetActionResultRequest{
		InstanceName: c.instanceName,
		ActionDigest: actionDigest.ToProto(),
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("GetActionResult: %w", err)
	}
	return resp, nil
}

// UpdateActionResult stores an ActionResult keyed by action digest.
func (c *Client) UpdateActionResult(ctx context.Context, actionDigest Digest, result *repb.ActionResult) error {
	rCtx, cancel := c.rpcCtx(ctx)
	defer cancel()

	acClient := repb.NewActionCacheClient(c.conn)
	_, err := acClient.UpdateActionResult(rCtx, &repb.UpdateActionResultRequest{
		InstanceName: c.instanceName,
		ActionDigest: actionDigest.ToProto(),
		ActionResult: result,
	})
	if err != nil {
		return fmt.Errorf("UpdateActionResult: %w", err)
	}
	return nil
}
