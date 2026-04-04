package reapi

import (
	"sync"

	repb "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"google.golang.org/protobuf/proto"
)

// Cached empty directory digest - never changes.
var (
	cachedDirData   []byte
	cachedDirDigest Digest
)

func init() {
	dir := EmptyDirectory()
	data, digest, err := MarshalDeterministic(dir)
	if err != nil {
		panic("failed to marshal empty directory: " + err.Error())
	}
	cachedDirData = data
	cachedDirDigest = digest
}

// SyntheticCommand builds a deterministic Command proto from an ActionID hex string.
// Command { arguments: ["gocacheprog", "<actionIDHex>"] }
func SyntheticCommand(actionIDHex string) *repb.Command {
	return &repb.Command{
		Arguments: []string{"gocacheprog", actionIDHex},
	}
}

// EmptyDirectory returns the canonical empty Directory proto.
func EmptyDirectory() *repb.Directory {
	return &repb.Directory{}
}

// SyntheticAction builds a deterministic Action proto referencing the Command
// and empty input root digests.
func SyntheticAction(commandDigest, inputRootDigest Digest) *repb.Action {
	return &repb.Action{
		CommandDigest:   commandDigest.ToProto(),
		InputRootDigest: inputRootDigest.ToProto(),
	}
}

// SyntheticActionResult builds an ActionResult with a single output file
// carrying the body blob digest at path = outputIDHex.
func SyntheticActionResult(outputIDHex string, bodyDigest Digest) *repb.ActionResult {
	return &repb.ActionResult{
		OutputFiles: []*repb.OutputFile{
			{
				Path:         outputIDHex,
				Digest:       bodyDigest.ToProto(),
				IsExecutable: false,
			},
		},
	}
}

// MarshalDeterministic serializes a proto message deterministically and returns
// its digest.
func MarshalDeterministic(msg proto.Message) ([]byte, Digest, error) {
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
	if err != nil {
		return nil, Digest{}, err
	}
	return data, DigestBytes(data), nil
}

// ActionDigestFromActionID computes the full chain:
// ActionID -> Command -> Action -> Action digest.
// It also returns the intermediate digests needed for CAS uploads.
type SyntheticDigests struct {
	CommandData   []byte
	CommandDigest Digest
	DirData       []byte
	DirDigest     Digest
	ActionData    []byte
	ActionDigest  Digest
}

// syntheticCache caches computed SyntheticDigests by actionIDHex to avoid
// repeated proto marshal + SHA-256 for the same action ID.
var syntheticCache sync.Map

// ComputeSyntheticDigests computes all synthetic proto digests from an ActionID hex string.
// Results are cached since the mapping is deterministic.
func ComputeSyntheticDigests(actionIDHex string) (*SyntheticDigests, error) {
	if v, ok := syntheticCache.Load(actionIDHex); ok {
		return v.(*SyntheticDigests), nil
	}

	cmd := SyntheticCommand(actionIDHex)
	cmdData, cmdDigest, err := MarshalDeterministic(cmd)
	if err != nil {
		return nil, err
	}

	// Use cached empty directory digest - it never changes.
	action := SyntheticAction(cmdDigest, cachedDirDigest)
	actionData, actionDigest, err := MarshalDeterministic(action)
	if err != nil {
		return nil, err
	}

	sd := &SyntheticDigests{
		CommandData:   cmdData,
		CommandDigest: cmdDigest,
		DirData:       cachedDirData,
		DirDigest:     cachedDirDigest,
		ActionData:    actionData,
		ActionDigest:  actionDigest,
	}
	syntheticCache.Store(actionIDHex, sd)
	return sd, nil
}
