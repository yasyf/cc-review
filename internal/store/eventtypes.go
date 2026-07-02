package store

// Event type strings carried in events.type. They form the tagged union the
// browser SPA and the Claude-side stream consumers switch on.
const (
	EventCommentCreated      = "comment.created"
	EventCommentUpdated      = "comment.updated"
	EventCommentResolved     = "comment.resolved"
	EventClaudeQuestion      = "claude.question"
	EventClaudeAsk           = "claude.ask"
	EventClaudeClarification = "claude.clarification"
	EventSubmit              = "submit"
	EventFileStates          = "file.states"
	EventVersionCreated      = "version.created"
	EventAIRequestCreated    = "ai.request.created"
	EventAIRequestUpdated    = "ai.request.updated"
	EventOrganizationUpdated = "organization.updated"
	EventAnnotationsUpdated  = "annotations.updated"
	EventChannelChanged      = "channel.changed"
	EventStatusChanged       = "status.changed"
)

// Origins recorded in events.origin.
const (
	OriginUser   = "user"
	OriginClaude = "claude"
	OriginSystem = "system"
)
