package api

// Stable machine-readable codes. The frontend renders copy from its own dictionary keyed by these;
// the human-readable "error" / "message" field stays as-is for curl and non-browser consumers.
// Not all of them ride an error response: empty_pr and the steer_rerunning_* pair key SSE info frames.
// Every constant here needs an entry in BOTH frontend dictionaries — see docs/API.md's i18n invariants.
const (
	CodeNotLoggedIn          = "not_logged_in"
	CodeOAuthNotConfigured   = "oauth_not_configured"
	CodeUnknownModel         = "unknown_model"
	CodePRNotFound           = "pr_not_found"
	CodeGitHubForbidden      = "github_forbidden"
	CodeNoPushPermission     = "no_push_permission"
	CodeNoCommentPermission  = "no_comment_permission"
	CodeSuggestionNoAnchor   = "suggestion_missing_anchor"
	CodeSuggestionNoPatch    = "suggestion_missing_patch"
	CodeEmptyPR              = "empty_pr"
	CodeHistoryLoginRequired = "history_login_required"
	CodeNotReviewOwner       = "not_review_owner"
	CodeAgentMaxSteps        = "agent_max_steps"
	// One code per rerunnable stage: byCode dictionary entries are plain strings with no interpolation,
	// so the stage name has to live in the code rather than in a placeholder.
	CodeSteerRerunningRisks       = "steer_rerunning_risks"
	CodeSteerRerunningSuggestions = "steer_rerunning_suggestions"
)

// steerRerunCodeByStage keys the stage-rerun info frame; the stage has already passed allowedSteerStages.
var steerRerunCodeByStage = map[string]string{
	"risks":       CodeSteerRerunningRisks,
	"suggestions": CodeSteerRerunningSuggestions,
}
