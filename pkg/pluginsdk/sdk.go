// Package pluginsdk is the public SDK for EasyCloud backend plugins.
//
// A backend plugin is a separate executable started by EasyCloud. It talks to
// the host over gRPC on a local socket (hashicorp/go-plugin). Messages are
// JSON-encoded, so no protobuf toolchain is needed to build a plugin:
//
//	func main() { pluginsdk.Serve(myExtension{}) }
//
// Capabilities (which MIME types get thumbnails, whether file events are
// wanted) are declared in the plugin's manifest.json, not over RPC.
package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/encoding"
)

// ProtocolVersion is bumped on incompatible RPC changes.
const ProtocolVersion = 1

// Handshake prevents running a plugin binary directly or with the wrong host.
var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  ProtocolVersion,
	MagicCookieKey:   "EASYCLOUD_PLUGIN",
	MagicCookieValue: "7b1f0c9e-easycloud-backend-plugin",
}

// ConfigEnv carries the plugin's JSON configuration (from the Dashboard).
const ConfigEnv = "EASYCLOUD_PLUGIN_CONFIG"

// ThumbnailRequest asks for a preview image of a file. Path is absolute and has
// already been authorized by the host; the plugin must only read it.
type ThumbnailRequest struct {
	Path string `json:"path"`
	Mime string `json:"mime"`
	Size int    `json:"size"` // longest edge in pixels
}

type ThumbnailResponse struct {
	Image []byte `json:"image"` // JPEG or PNG bytes
	Mime  string `json:"mime"`
}

// FileEvent reports a change in the data root (relative display path, e.g. "/share/a.mp4").
type FileEvent struct {
	Kind string   `json:"kind"`
	Dirs []string `json:"dirs"`
}

type Empty struct{}

// Account is a protocol login. NTHash is the Samba/smbpasswd NT hash (upper-case hex)
// of the user's most recent app password.
type Account struct {
	Username string `json:"username"`
	NTHash   string `json:"ntHash"`
	Admin    bool   `json:"admin"`
}

// ShareDef is one exported directory. Path is relative to the data root ("" = root).
type ShareDef struct {
	Name       string   `json:"name"`
	Path       string   `json:"path"`
	ReadUsers  []string `json:"readUsers"`
	WriteUsers []string `json:"writeUsers"`
}

// SyncRequest is the complete desired state; plugins replace, not merge.
type SyncRequest struct {
	DataRoot string     `json:"dataRoot"` // absolute path on the EasyCloud host/container
	Accounts []Account  `json:"accounts"`
	Shares   []ShareDef `json:"shares"`
}

// ErrUnsupported tells the host to fall back to another provider.
var ErrUnsupported = errors.New("unsupported")

// Extension is implemented by backend plugins. Embed Base to get defaults.
type Extension interface {
	Thumbnail(ctx context.Context, req *ThumbnailRequest) (*ThumbnailResponse, error)
	OnEvent(ctx context.Context, ev *FileEvent) (*Empty, error)
	// Sync receives accounts and shares (plugins with "accounts": true in their manifest).
	Sync(ctx context.Context, req *SyncRequest) (*Empty, error)
}

// Base provides no-op implementations.
type Base struct{}

func (Base) Thumbnail(context.Context, *ThumbnailRequest) (*ThumbnailResponse, error) {
	return nil, ErrUnsupported
}
func (Base) OnEvent(context.Context, *FileEvent) (*Empty, error) { return &Empty{}, nil }
func (Base) Sync(context.Context, *SyncRequest) (*Empty, error)  { return &Empty{}, nil }

// Config decodes the plugin configuration passed by the host.
func Config(v any) error {
	raw := os.Getenv(ConfigEnv)
	if raw == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), v)
}

// ---- gRPC plumbing (JSON codec + hand-written service descriptor) ----

type jsonCodec struct{}

func (jsonCodec) Marshal(v any) ([]byte, error)   { return json.Marshal(v) }
func (jsonCodec) Unmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }
func (jsonCodec) Name() string                    { return "json" }

func init() { encoding.RegisterCodec(jsonCodec{}) }

const serviceName = "easycloud.plugin.v1.Extension"

var serviceDesc = grpc.ServiceDesc{
	ServiceName: serviceName,
	HandlerType: (*Extension)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Thumbnail", Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			in := new(ThumbnailRequest)
			if err := dec(in); err != nil {
				return nil, err
			}
			return srv.(Extension).Thumbnail(ctx, in)
		}},
		{MethodName: "OnEvent", Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			in := new(FileEvent)
			if err := dec(in); err != nil {
				return nil, err
			}
			return srv.(Extension).OnEvent(ctx, in)
		}},
		{MethodName: "Sync", Handler: func(srv any, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			in := new(SyncRequest)
			if err := dec(in); err != nil {
				return nil, err
			}
			return srv.(Extension).Sync(ctx, in)
		}},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "easycloud/plugin/v1",
}

// GRPCPlugin adapts an Extension to go-plugin.
type GRPCPlugin struct {
	plugin.NetRPCUnsupportedPlugin
	Impl Extension
}

func (p *GRPCPlugin) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	s.RegisterService(&serviceDesc, p.Impl)
	return nil
}

func (p *GRPCPlugin) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return &client{conn: c}, nil
}

// PluginMap is the dispense table shared by host and plugin.
func PluginMap(impl Extension) map[string]plugin.Plugin {
	return map[string]plugin.Plugin{"extension": &GRPCPlugin{Impl: impl}}
}

type client struct{ conn *grpc.ClientConn }

func (c *client) Thumbnail(ctx context.Context, req *ThumbnailRequest) (*ThumbnailResponse, error) {
	out := new(ThumbnailResponse)
	err := c.conn.Invoke(ctx, "/"+serviceName+"/Thumbnail", req, out, grpc.CallContentSubtype("json"), grpc.MaxCallRecvMsgSize(64<<20))
	return out, err
}

func (c *client) Sync(ctx context.Context, req *SyncRequest) (*Empty, error) {
	out := new(Empty)
	err := c.conn.Invoke(ctx, "/"+serviceName+"/Sync", req, out, grpc.CallContentSubtype("json"))
	return out, err
}

func (c *client) OnEvent(ctx context.Context, ev *FileEvent) (*Empty, error) {
	out := new(Empty)
	err := c.conn.Invoke(ctx, "/"+serviceName+"/OnEvent", ev, out, grpc.CallContentSubtype("json"))
	return out, err
}

// Serve runs the plugin; call it from main.
func Serve(impl Extension) {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         PluginMap(impl),
		GRPCServer:      plugin.DefaultGRPCServer,
	})
}
