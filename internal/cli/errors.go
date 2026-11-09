package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// Exit codes.
const (
	exitOK       = 0
	exitFailure  = 1   // operational failure, including partial failure
	exitUsage    = 2   // usage error or missing input
	exitCanceled = 130 // canceled by the user, including declining a confirmation
)

const (
	statusError          = "error"
	statusActionRequired = "action_required"
)

// Recovery is one way to recover, rendered as "<Instruction>:" and an indented command.
// Without Argv, Instruction is printed as a plain sentence.
type Recovery struct {
	Instruction string
	Argv        []string
	When        string // JSON only: the condition for running it
}

type detail struct{ label, value string }

// Error is the one error type the CLI renders. Its class fixes Exit.
type Error struct {
	Exit               int
	Status             string
	Reason             string // stable snake_case code
	Message            string // headline, no glyph
	Details            []detail
	Recovery           []Recovery
	Data               map[string]any
	UserActionRequired bool
	Retryable          bool
	RetryAfter         int
	RequestID          string
}

func (e *Error) Error() string { return e.Message }

func (e *Error) addDetail(label, value string) {
	e.Details = append(e.Details, detail{label, value})
}

func (e *Error) setData(k string, v any) {
	if e.Data == nil {
		e.Data = map[string]any{}
	}
	e.Data[k] = v
}

type nextJSON struct {
	Command string   `json:"command"`
	Argv    []string `json:"argv"`
	When    string   `json:"when,omitempty"`
}

func nextList(rs []Recovery) []nextJSON {
	var out []nextJSON
	for _, r := range rs {
		if len(r.Argv) > 0 {
			out = append(out, nextJSON{Command: joinArgv(r.Argv), Argv: r.Argv, When: r.When})
		}
	}
	return out
}

// renderJSON writes the error as one line.
func (e *Error) renderJSON() string {
	doc := map[string]any{"status": e.Status, "reason": e.Reason, "message": e.Message}
	if n := nextList(e.Recovery); len(n) > 0 {
		doc["next"] = n
	}
	if len(e.Data) > 0 {
		doc["data"] = e.Data
	}
	if e.UserActionRequired {
		doc["userActionRequired"] = true
	}
	if e.Retryable {
		doc["retryable"] = true
	}
	if e.RetryAfter > 0 {
		doc["retryAfter"] = e.RetryAfter
	}
	if e.RequestID != "" {
		doc["requestId"] = e.RequestID
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(doc)
	return strings.TrimSuffix(b.String(), "\n")
}

// renderHuman writes the headline, details, IDs and up to two recovery options.
func (e *Error) renderHuman(p printer) {
	p.errorHeadline(e.Message)
	for _, d := range e.Details {
		p.row(d.label, d.value)
	}
	if e.RequestID != "" {
		p.row("Request ID", e.RequestID)
	}
	for i, r := range e.Recovery {
		if i == 2 {
			break
		}
		p.blank()
		if len(r.Argv) == 0 {
			p.text(r.Instruction)
			continue
		}
		p.text(r.Instruction + ":")
		p.command(joinArgv(r.Argv))
	}
}

// apiContext names what a failed call acted on, for the headline.
type apiContext struct {
	verb     string // "find", "add", "list"
	object   string // "issue or pull request 5 in o/r"
	notFound *Error // headline and recovery for a 404, when the caller has a better one
	mutation bool   // a failure after sending may have changed something
}

// mapAPIError maps a GitHub API failure to an error class, in one place.
// Remote messages appear only in an attributed detail row and data.remoteMessage.
func mapAPIError(err error, c apiContext, getenv func(string) string) *Error {
	var cliErr *Error
	if errors.As(err, &cliErr) {
		return cliErr
	}
	var httpErr *api.HTTPError
	var gqlErr *api.GraphQLError
	switch {
	case errors.As(err, &httpErr):
		return mapHTTPError(httpErr, c, getenv)
	case errors.As(err, &gqlErr):
		return mapGraphQLError(gqlErr, c)
	}
	var netErr net.Error
	var urlErr *url.Error
	if errors.As(err, &netErr) || errors.As(err, &urlErr) {
		host := "api.github.com"
		if urlErr != nil {
			if u, perr := url.Parse(urlErr.URL); perr == nil && u.Host != "" {
				host = u.Host
			}
		}
		cause := "The connection failed."
		if netErr != nil && netErr.Timeout() {
			cause = "The request timed out."
		}
		if c.mutation {
			return unknownOutcome(c)
		}
		return &Error{Exit: exitFailure, Status: statusError, Reason: "network_error", Retryable: true,
			Message: fmt.Sprintf("Couldn't reach %s. %s", host, cause)}
	}
	if isNoToken(err) {
		return loginRequired(getenv)
	}
	return &Error{Exit: exitFailure, Status: statusError, Reason: "api_error",
		Message: fmt.Sprintf("Failed to %s %s.", c.verb, c.object), Details: []detail{{"Error", sanitize(err.Error())}}}
}

func isNoToken(err error) bool {
	return strings.Contains(err.Error(), "authentication token not found")
}

func loginRequired(getenv func(string) string) *Error {
	e := &Error{Exit: exitFailure, Status: statusError, Reason: "login_required", UserActionRequired: true,
		Message: "You're not logged in to GitHub."}
	if getenv("CI") != "" {
		e.Recovery = []Recovery{{Instruction: "Set GH_TOKEN to a token with access to the project and repositories."}}
		return e
	}
	e.Recovery = []Recovery{{Instruction: "Log in with the project scope", Argv: []string{"gh", "auth", "login", "--scopes", "project"}}}
	return e
}

func remoteDetail(e *Error, msg string) *Error {
	if msg = sanitize(strings.TrimSpace(msg)); msg != "" {
		e.addDetail("GitHub", msg)
		e.setData("remoteMessage", msg)
	}
	return e
}

func mapHTTPError(h *api.HTTPError, c apiContext, getenv func(string) string) *Error {
	reqID := h.Headers.Get("X-GitHub-Request-Id")
	switch {
	case h.StatusCode == http.StatusUnauthorized:
		e := loginRequired(getenv)
		e.Reason, e.Message = "token_invalid", "GitHub didn't accept the token."
		if getenv("GH_TOKEN") != "" || getenv("GITHUB_TOKEN") != "" {
			e.Recovery = []Recovery{{Instruction: "Check the token in GH_TOKEN or GITHUB_TOKEN. It may be expired or revoked."}}
		}
		return e
	case h.StatusCode == http.StatusTooManyRequests || (h.StatusCode == http.StatusForbidden && h.Headers.Get("X-RateLimit-Remaining") == "0"):
		return rateLimited(h)
	case h.StatusCode == http.StatusForbidden:
		return remoteDetail(&Error{Exit: exitFailure, Status: statusError, Reason: "permission_denied",
			Message:  fmt.Sprintf("This token can't %s %s.", c.verb, c.object),
			Recovery: []Recovery{{Instruction: "Check which account and scopes gh is using", Argv: []string{"gh", "auth", "status"}}}}, h.Message)
	case h.StatusCode == http.StatusNotFound:
		if c.notFound != nil {
			return c.notFound
		}
		return &Error{Exit: exitFailure, Status: statusError, Reason: "not_found",
			Message: fmt.Sprintf("Couldn't find %s.", c.object)}
	case h.StatusCode >= http.StatusInternalServerError && c.mutation:
		e := unknownOutcome(c)
		e.RequestID = reqID
		return e
	}
	e := remoteDetail(&Error{Exit: exitFailure, Status: statusError, Reason: "api_error", RequestID: reqID,
		Message: fmt.Sprintf("Failed to %s %s. GitHub returned status %d.", c.verb, c.object, h.StatusCode)}, h.Message)
	e.Retryable = h.StatusCode >= http.StatusInternalServerError
	return e
}

func rateLimited(h *api.HTTPError) *Error {
	wait := 60
	if s, err := strconv.Atoi(h.Headers.Get("Retry-After")); err == nil && s > 0 {
		wait = s
	} else if reset, err := strconv.ParseInt(h.Headers.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		if d := int(time.Until(time.Unix(reset, 0)).Seconds()); d > 0 {
			wait = d
		}
	}
	return &Error{Exit: exitFailure, Status: statusError, Reason: "rate_limited", Retryable: true, RetryAfter: wait,
		Message: fmt.Sprintf("Too many requests to the GitHub API. Try again in %s.", humanDuration(wait))}
}

func humanDuration(sec int) string {
	switch {
	case sec < 60:
		return plural(sec, "second", "seconds")
	case sec < 3600:
		return plural((sec+59)/60, "minute", "minutes")
	}
	return plural((sec+3599)/3600, "hour", "hours")
}

func mapGraphQLError(g *api.GraphQLError, c apiContext) *Error {
	var msgs []string
	kind := ""
	for _, item := range g.Errors {
		msgs = append(msgs, item.Message)
		if kind == "" {
			kind = item.Type
		}
	}
	remote := strings.Join(msgs, " ")
	switch kind {
	case "INSUFFICIENT_SCOPES":
		return remoteDetail(&Error{Exit: exitFailure, Status: statusError, Reason: "token_scope_required", UserActionRequired: true,
			Message:  fmt.Sprintf("This token can't %s %s. It needs the project scope.", c.verb, c.object),
			Recovery: []Recovery{{Instruction: "Add the project scope to your gh login", Argv: []string{"gh", "auth", "refresh", "--scopes", "project"}}}}, remote)
	case "NOT_FOUND":
		if c.notFound != nil {
			return c.notFound
		}
		return &Error{Exit: exitFailure, Status: statusError, Reason: "not_found", Message: fmt.Sprintf("Couldn't find %s.", c.object)}
	case "FORBIDDEN":
		return remoteDetail(&Error{Exit: exitFailure, Status: statusError, Reason: "permission_denied",
			Message:  fmt.Sprintf("This token can't %s %s.", c.verb, c.object),
			Recovery: []Recovery{{Instruction: "Check which account and scopes gh is using", Argv: []string{"gh", "auth", "status"}}}}, remote)
	case "RATE_LIMITED":
		return &Error{Exit: exitFailure, Status: statusError, Reason: "rate_limited", Retryable: true,
			Message: "Too many requests to the GitHub API. Try again later."}
	}
	return remoteDetail(&Error{Exit: exitFailure, Status: statusError, Reason: "api_error",
		Message: fmt.Sprintf("Failed to %s %s.", c.verb, c.object)}, remote)
}

// unknownOutcome reports a mutation that may or may not have happened. Adding an item is
// idempotent, so running the same command again is safe and never duplicates it.
func unknownOutcome(c apiContext) *Error {
	return &Error{Exit: exitFailure, Status: statusError, Reason: "unknown_outcome", Retryable: true,
		Message:  fmt.Sprintf("Can't confirm whether %s was added. The request didn't complete.", c.object),
		Recovery: []Recovery{{Instruction: "Adding is idempotent. Run the same command again to make sure it's added."}}}
}
