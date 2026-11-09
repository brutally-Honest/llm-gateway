package core

import "time"

// Effect is something a session did outside the model exchange: a file that changed on
// disk, a file the harness's own tools read, or a network connection made through the
// gateway's forward proxy. It is a canonical kind of its own, stored in its own table
// (ADR 0014). Every field is harness-neutral; a field a kind does not use is left zero.
type Effect struct {
	Kind EffectKind
	At   time.Time // when the effect was seen: the checkpoint, the hook, the connection's start

	// Session identity. An empty SessionKey is an unassigned effect.
	Principal  string
	Client     string
	SessionKey string

	// File change and file read.
	Root       string     // the watched folder the path is in; "" for a read
	Path       string     // absolute path
	Change     FileChange // file change only
	Size       int64      // bytes after the change; 0 for a delete
	Mode       string     // "git" or "scan": how the change was found
	AfterHash  string     // content hash of the stored copy after the change; "" when none
	BeforeHash string     // content hash of the version before the change; "" when none
	HookID     int64      // file read: the stored hook event its content comes from
	// Detail is a read's line range or pattern, or a plain HTTP request's method and
	// URL without its query string.
	Detail string

	// Network connection.
	Host      string
	Port      int
	Via       string // "connect", "http" or "socks"
	BytesUp   int64
	BytesDown int64
	EndedAt   time.Time
	Outcome   NetOutcome
}

// EffectKind names what an effect is.
type EffectKind string

// The effect kinds.
const (
	EffectFileChange EffectKind = "file_change"
	EffectFileRead   EffectKind = "file_read"
	EffectNetConn    EffectKind = "net_conn"
)

// FileChange is how a file changed between two checkpoints. A rename is a delete and a
// create.
type FileChange string

// The file changes.
const (
	FileCreated  FileChange = "created"
	FileModified FileChange = "modified"
	FileDeleted  FileChange = "deleted"
)

// NetOutcome is how a proxied connection ended.
type NetOutcome string

// The connection outcomes.
const (
	NetOK          NetOutcome = "ok"
	NetRefused     NetOutcome = "refused"
	NetError       NetOutcome = "error"
	NetClosedEarly NetOutcome = "closed early"
)

// EffectRules is an optional profile interface. A profile that implements it tells the
// effect sensors which of its harness's tools read or write files, and which extra
// folders its harness works in. The tool names and input shapes live only in that
// profile.
type EffectRules interface {
	// ReadTarget reports the path a completed read, search or list tool call looked at,
	// with its line range or pattern as detail; ok is false for any other tool.
	ReadTarget(tool string, input []byte) (path, detail string, ok bool)
	// ReadContent pulls what a read tool returned out of a stored post-tool payload.
	ReadContent(payload []byte) ([]byte, bool)
	// WritePaths are the paths a write tool call names; none for any other tool.
	WritePaths(tool string, input []byte) []string
	// ExtraDirs are the extra folders the harness's own settings add for a session
	// started in workDir.
	ExtraDirs(workDir string) []string
}
