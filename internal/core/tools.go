package core

// ToolKind says what a tool call does, without naming a tool. The session view uses it to
// list commands, file tools and searches; a profile decides which tool is which kind.
type ToolKind string

// The tool kinds.
const (
	ToolCommand ToolKind = "command" // runs a shell command
	ToolFile    ToolKind = "file"    // reads or writes one file
	ToolSearch  ToolKind = "search"  // searches or lists files
	ToolFetch   ToolKind = "fetch"   // fetches a URL or searches the web
	ToolSkill   ToolKind = "skill"   // runs a skill
	ToolAgent   ToolKind = "agent"   // spawns a sub-agent
	ToolOther   ToolKind = "other"   // anything else: shown by its name alone
)

// ToolSummary is one tool call as a line: what it acted on and, for an edit, the lines it
// adds and removes. The view shows Arg, then Path made relative to the session's folder:
// Arg alone when Path is empty, the path alone when Arg is empty, else "Arg in path".
type ToolSummary struct {
	Kind ToolKind
	// Arg is the input's main argument as text: a command, a pattern, a URL without its
	// scheme, a skill name; "" when the line shows only a path.
	Arg string
	// Path is the absolute path the call names; "" when none.
	Path string
	// Added and Removed are the lines an edit adds and removes; -1 when not known.
	Added, Removed int
}

// ToolSummarizer is an optional profile interface. A profile that implements it turns a
// tool call's name and input into a ToolSummary; the tool names and input shapes live only
// in that profile. A client without it gets tool names alone.
type ToolSummarizer interface {
	// SummarizeTool summarises one call. workDir is the folder the call ran in, used to
	// make a relative path in the input absolute; "" when unknown.
	SummarizeTool(name string, input []byte, workDir string) ToolSummary
}
