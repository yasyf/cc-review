package store

// Event type strings carried in events.type. They form the tagged union the
// browser SPA and the Claude-side stream consumers switch on.
const (
	EventCommentCreated      = "comment.created"
	EventCommentUpdated      = "comment.updated"
	EventCommentResolved     = "comment.resolved"
	EventClaudeQuestion      = "claude.question"
	EventClaudeOption        = "claude.option"
	EventClaudeClarification = "claude.clarification"
	EventSubmit              = "submit"
)

// Origins recorded in events.origin.
const (
	OriginUser   = "user"
	OriginClaude = "claude"
	OriginSystem = "system"
)
