package timeline

import (
	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// The session view (spec 016, ADR 0016): one session as the page and `gateway timeline`
// show it. BuildView computes every fact once; the browser and the CLI only format it.
// Times are RFC 3339 in UTC, durations milliseconds; a nil pointer is unknown, never 0.

// ViewRules is the client knowledge the view needs; a nil ViewRules means no side calls,
// prompts taken whole, no generated titles and no agent tools.
type ViewRules interface {
	FactRules
	// SplitReminders returns a prompt as the text the user wrote and its reminder blocks.
	SplitReminders(text string) (body string, reminders []string)
	// TitleOf extracts a generated title from a response; false when unusable.
	TitleOf(responseText string) (string, bool)
}

// SideCallNamer is optional on ViewRules: it names a side call in words ("quota probe").
type SideCallNamer interface {
	SideCallName(userTexts []string) string
}

// EffectLabel is an effect's attribution (spec 015) as the view reads it.
type EffectLabel struct {
	Label      string
	Tool       *LabelTool
	Candidates []LabelTool
	Client     string
}

// LabelTool names one tool call an effect label points at.
type LabelTool struct{ ID, Name, AgentID string }

// UnassignedConn is a connection the gateway could not give a session, seen while this
// session ran (spec 016 AC9).
type UnassignedConn struct {
	Host  string
	Port  int
	At    int64
	Label EffectLabel
}

// ViewInput is everything BuildView reads. Entries, Facts and Exchanges are one timeline
// build; Hooks are the session's hook rows; Labels[i] is Effects[i]'s attribution.
type ViewInput struct {
	Session    store.SessionRow
	Entries    []Entry
	Facts      map[string]*CallFact
	Exchanges  map[string]store.ExchangeRow
	Hooks      []store.HookRow
	Effects    []store.EffectRow
	Labels     []EffectLabel
	Unassigned []UnassignedConn
	Rules      ViewRules
	Summarizer core.ToolSummarizer
	Prompts    core.PromptReader
	// Content reads stored content by hash (hook tool inputs and prompt payloads); nil
	// reads nothing.
	Content func(hash string) []byte
	// Lines gives a file change's lines added and removed from its stored versions; ok is
	// false when they are not known. Nil knows none.
	Lines func(e store.EffectRow) (added, removed int, ok bool)
	// Active is true while the session is active (its last activity is recent).
	Active bool
	// Home is the gateway user's home folder, shown as "~"; "" shows paths whole.
	Home    string
	Version string
}

// Mark is a status as glyph and word; the colour only ever repeats it.
type Mark struct {
	Glyph string `json:"glyph"`
	Word  string `json:"word"`
}

// View is one session's page.
type View struct {
	Version   string       `json:"version"`
	Overview  Overview     `json:"overview"`
	SideCalls []SideCall   `json:"side_calls"`
	Turns     []Turn       `json:"turns"`
	Between   []Between    `json:"between"`
	Files     FilesList    `json:"files"`
	Commands  CommandsList `json:"commands"`
	Sites     SitesList    `json:"sites"`
	Context   ContextView  `json:"context"`
}

// Overview is the top of the page (AC2).
type Overview struct {
	Title       string     `json:"title"`
	TitleSource string     `json:"title_source"` // generated, prompt, hooks_only, no_prompt
	TitleCall   int        `json:"title_call,omitempty"`
	Status      Mark       `json:"status"`
	From        string     `json:"from,omitempty"`
	To          string     `json:"to,omitempty"`
	Folder      string     `json:"folder,omitempty"`
	FolderFull  string     `json:"folder_full,omitempty"`
	Models      []string   `json:"models"`
	Client      string     `json:"client"`
	Key         string     `json:"key"`
	Turns       int        `json:"turns"`
	Calls       int        `json:"calls"`
	Time        TimeCard   `json:"time"`
	Files       FilesCard  `json:"files"`
	Commands    CmdCard    `json:"commands"`
	Sites       SitesCard  `json:"sites"`
	Context     CtxCard    `json:"context"`
	NeedsLook   []LookItem `json:"needs_look"`
	MoreLook    int        `json:"more_look"`
	Tokens      TokenLine  `json:"tokens"`
	// Empty is the empty-state line of a session with no turns; "" otherwise.
	Empty string `json:"empty,omitempty"`
}

// TimeCard is the session's clock time and its model and tool parts.
type TimeCard struct {
	ClockMS int64 `json:"clock_ms"`
	ModelMS int64 `json:"model_ms"`
	ToolMS  int64 `json:"tool_ms"`
}

// FilesCard counts changed and read files; Added and Removed sum the known line counts.
type FilesCard struct {
	Changed int `json:"changed"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Read    int `json:"read"`
}

// CmdCard counts shell commands and names the longest.
type CmdCard struct {
	Count     int    `json:"count"`
	Longest   string `json:"longest,omitempty"`
	LongestMS *int64 `json:"longest_ms,omitempty"`
}

// SitesCard counts hosts and connections.
type SitesCard struct {
	Count       int `json:"count"`
	Connections int `json:"connections"`
}

// CtxCard is the last main-thread call's context.
type CtxCard struct {
	Last *int64 `json:"last"`
}

// TokenLine sums every call's tokens; Incomplete is true when some call did not report.
type TokenLine struct {
	Input      *int64 `json:"input"`
	Output     *int64 `json:"output"`
	CacheWrite *int64 `json:"cache_write"`
	CacheRead  *int64 `json:"cache_read"`
	Incomplete bool   `json:"incomplete"`
}

// LookItem is one "Needs a look" entry; Target says what it opens or where it scrolls.
type LookItem struct {
	Tone   string `json:"tone"` // bad, warn, info
	Glyph  string `json:"glyph"`
	Title  string `json:"title"`
	Sub    string `json:"sub"`
	Go     string `json:"go"`
	Target Target `json:"target"`
}

// Target is a place on the page: a tool, file, site or call to open, or a turn, a
// between block or a several-running group to show.
type Target struct {
	Kind string `json:"kind,omitempty"` // tool, file, site, call, turn, between, several
	ID   string `json:"id,omitempty"`
	Turn int    `json:"turn,omitempty"`
}

// SideCall is one call the client makes on its own behalf: never a turn, never an error.
type SideCall struct {
	N          int    `json:"n"`
	ExchangeID string `json:"exchange_id"`
	Name       string `json:"name"`
	At         string `json:"at"`
	DurMS      int64  `json:"dur_ms"`
	Status     string `json:"status"`
}

// Turn is one prompt and what followed it (AC4).
type Turn struct {
	N               int          `json:"n"`
	Hash            string       `json:"hash"`
	Prompt          string       `json:"prompt"`
	PromptTruncated bool         `json:"prompt_truncated,omitempty"`
	Reminders       []string     `json:"reminders,omitempty"`
	At              string       `json:"at"`
	End             *Mark        `json:"end,omitempty"` // set when the turn has no final answer
	ClockMS         int64        `json:"clock_ms"`
	ModelMS         int64        `json:"model_ms"`
	ToolMS          int64        `json:"tool_ms"`
	WaitMS          int64        `json:"wait_ms"`
	HooksOnly       bool         `json:"hooks_only,omitempty"`
	Counts          TurnCounts   `json:"counts"`
	Steps           []Step       `json:"steps,omitempty"`
	HookTools       []ToolLine   `json:"hook_tools,omitempty"`
	Loose           []EffectLine `json:"loose,omitempty"`
	Answer          string       `json:"answer,omitempty"`
	AnswerCall      int          `json:"answer_call,omitempty"`
	atNS            int64
}

// TurnCounts are a turn's pills.
type TurnCounts struct {
	Tools    int `json:"tools,omitempty"`
	Changed  int `json:"changed"`
	Read     int `json:"read"`
	Searched int `json:"searched"`
	Sites    int `json:"sites"`
	Failed   int `json:"failed"`
}

// Step is one model call in a turn (AC7).
type Step struct {
	N            int          `json:"n"`
	ExchangeID   string       `json:"exchange_id"`
	At           string       `json:"at"`
	DurMS        int64        `json:"dur_ms"`
	Did          string       `json:"did"`
	Errored      bool         `json:"errored,omitempty"`
	Status       string       `json:"status,omitempty"`
	Thinking     bool         `json:"thinking,omitempty"`
	Agent        string       `json:"agent,omitempty"`
	Tools        []ToolLine   `json:"tools,omitempty"`
	Several      []EffectLine `json:"several,omitempty"`
	SeveralNames []string     `json:"several_names,omitempty"`
}

// ToolLine is one tool call as a line (AC5).
type ToolLine struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	State      Mark         `json:"state"`
	Chip       string       `json:"chip,omitempty"`
	Kind       string       `json:"kind,omitempty"`
	Summary    string       `json:"summary,omitempty"`
	FullPath   string       `json:"full_path,omitempty"`
	Added      int          `json:"added"`
	Removed    int          `json:"removed"`
	DurMS      *int64       `json:"dur_ms"`
	Approx     bool         `json:"approx,omitempty"`
	FirstError string       `json:"first_error,omitempty"`
	CallN      int          `json:"call_n,omitempty"`
	Agent      string       `json:"agent,omitempty"`
	Short      string       `json:"short,omitempty"`
	Turn       int          `json:"turn,omitempty"`
	Effects    []EffectLine `json:"effects,omitempty"`
	Sub        *SubAgent    `json:"sub,omitempty"`
}

// SubAgent is the sub-agent an agent tool call spawned: its model and tool calls.
type SubAgent struct {
	Label string `json:"label"`
	Calls int    `json:"calls"`
	Steps []Step `json:"steps,omitempty"`
}

// EffectLine is one effect as a line (AC6).
type EffectLine struct {
	ID         int64    `json:"id"`
	Kind       string   `json:"kind,omitempty"` // change, read, search, net
	At         string   `json:"at"`
	What       Mark     `json:"what"`
	Path       string   `json:"path,omitempty"`
	FullPath   string   `json:"full_path,omitempty"`
	Change     string   `json:"change,omitempty"`
	Size       *int64   `json:"size,omitempty"`
	Added      int      `json:"added"`
	Removed    int      `json:"removed"`
	NoCopy     bool     `json:"no_copy,omitempty"`
	HasDiff    bool     `json:"has_diff,omitempty"`
	HasCopy    bool     `json:"has_copy,omitempty"`
	Detail     string   `json:"detail,omitempty"`
	Host       string   `json:"host,omitempty"`
	Port       int      `json:"port,omitempty"`
	Up         int64    `json:"up,omitempty"`
	Down       int64    `json:"down,omitempty"`
	DurMS      *int64   `json:"dur_ms,omitempty"`
	Outcome    string   `json:"outcome,omitempty"`
	Label      string   `json:"label,omitempty"`
	Mark       *Mark    `json:"mark,omitempty"`
	Tool       string   `json:"tool,omitempty"`
	By         string   `json:"by,omitempty"`
	Why        string   `json:"why,omitempty"`
	During     string   `json:"during,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	Turn       int      `json:"turn,omitempty"`
	Where      string   `json:"where,omitempty"`
}

// Between is an edit seen between two turns (AC8, D9).
type Between struct {
	After   int          `json:"after"`
	At      string       `json:"at"`
	Title   string       `json:"title"`
	Text    string       `json:"text"`
	Effects []EffectLine `json:"effects,omitempty"`
}

// FilesList is the Files tab (AC9).
type FilesList struct {
	Changed  []FileItem   `json:"changed"`
	Read     []ReadItem   `json:"read"`
	Searches []SearchItem `json:"searches"`
	Coverage string       `json:"coverage"`
	Empty    string       `json:"empty,omitempty"`
}

// FileItem is one changed path with all its changes.
type FileItem struct {
	Path     string       `json:"path"`
	FullPath string       `json:"full_path"`
	Added    int          `json:"added"`
	Removed  int          `json:"removed"`
	Lines    bool         `json:"lines"` // Added and Removed are known
	Last     EffectLine   `json:"last"`
	Changes  []EffectLine `json:"changes"`
	Reads    []EffectLine `json:"reads"`
}

// ReadItem is one file read, once however often.
type ReadItem struct {
	Path     string       `json:"path"`
	FullPath string       `json:"full_path"`
	Count    int          `json:"count"`
	Where    string       `json:"where,omitempty"`
	Reads    []EffectLine `json:"reads"`
}

// SearchItem is one search tool call.
type SearchItem struct {
	ToolID  string `json:"tool_id"`
	Name    string `json:"name"`
	Summary string `json:"summary,omitempty"`
	Where   string `json:"where,omitempty"`
	Agent   string `json:"agent,omitempty"`
}

// CommandsList is the Commands tab: every shell command in order.
type CommandsList struct {
	Items    []CommandItem `json:"items"`
	Coverage string        `json:"coverage"`
	Empty    string        `json:"empty,omitempty"`
}

// CommandItem is one shell command.
type CommandItem struct {
	ToolID    string `json:"tool_id"`
	Summary   string `json:"summary,omitempty"`
	State     Mark   `json:"state"`
	Chip      string `json:"chip,omitempty"`
	Turn      int    `json:"turn,omitempty"`
	DurMS     *int64 `json:"dur_ms"`
	Approx    bool   `json:"approx,omitempty"`
	Did       string `json:"did"`
	HooksOnly bool   `json:"hooks_only,omitempty"`
}

// SitesList is the Sites tab.
type SitesList struct {
	Items      []SiteItem      `json:"items"`
	Coverage   string          `json:"coverage"`
	Empty      string          `json:"empty,omitempty"`
	Unassigned *UnassignedNote `json:"unassigned,omitempty"`
}

// SiteItem is one host with its connections.
type SiteItem struct {
	Host        string       `json:"host"`
	Port        int          `json:"port"`
	Connections int          `json:"connections"`
	Up          int64        `json:"up"`
	Down        int64        `json:"down"`
	Where       string       `json:"where,omitempty"`
	Who         string       `json:"who"`
	Harness     bool         `json:"harness"`
	Conns       []EffectLine `json:"conns"`
}

// UnassignedNote names the unassigned connections seen while the session ran.
type UnassignedNote struct {
	Count int    `json:"count"`
	Host  string `json:"host"`
	At    string `json:"at"`
	Label string `json:"label"`
}

// ContextView is the Context tab (AC10).
type ContextView struct {
	Now      *int64      `json:"now"`
	First    *int64      `json:"first"`
	Growth   *int64      `json:"growth"`
	Peak     int64       `json:"peak"`
	Main     []CtxRow    `json:"main"`
	Subs     []CtxThread `json:"subs"`
	Coverage string      `json:"coverage"`
	Note     string      `json:"note,omitempty"`
}

// CtxThread is one sub-agent's own context rows.
type CtxThread struct {
	Label string   `json:"label"`
	Rows  []CtxRow `json:"rows"`
}

// CtxRow is one call's context and what it added since the previous call of its thread.
type CtxRow struct {
	N          int    `json:"n"`
	ExchangeID string `json:"exchange_id"`
	Turn       int    `json:"turn,omitempty"`
	Ctx        *int64 `json:"ctx"`
	Delta      *int64 `json:"delta"`
	Why        string `json:"why,omitempty"`
}
