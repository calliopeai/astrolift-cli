// Zentinelle reads refused by the credential, not by the account (#179).
//
// A bearer's scopes cap the account's roles, and the server checks that cap
// first: a refusal for tokenScopeRefusal never consulted the roles, so
// `astro perms diagnose` can show the grant while the read is still refused,
// and no role binding fixes it. Only another sign-in does. The default and
// clusters sign-ins carry no Zentinelle permission, so the refusal names the
// one that does. An account refusal is left as the server said it.

package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/calliopeai/astrolift-cli/internal/api"
	"github.com/spf13/cobra"
)

// tokenScopeRefusal is the server's reason when a credential's scopes, not
// the account's roles, refuse a permission.
const tokenScopeRefusal = "api token scope does not allow this permission"

// Zentinelle reads have their own explicitly approved ceiling. Export and writes
// retain the admin requirement.
const zentinelleLoginScope = "zentinelle"

func zentinelleScopeForPermission(permission string) string {
	switch permission {
	case "zentinelle.status_view", "zentinelle.policy_view", "zentinelle.usage_view", "zentinelle.conformance_view", "zentinelle.audit_view":
		return zentinelleLoginScope
	default:
		return "admin"
	}
}

// credentialAdvice is the credential field of a --json error.
type credentialAdvice struct {
	Permission string `json:"permission"`
	LoginScope string `json:"loginScope"`
	Hint       string `json:"hint"`
}

// credentialScopeError is a Zentinelle permission the credential's scopes refused.
type credentialScopeError struct {
	advice credentialAdvice
	err    error
}

func (e *credentialScopeError) Error() string { return e.err.Error() + "\n" + e.advice.Hint }
func (e *credentialScopeError) Unwrap() error { return e.err }

func newCredentialScopeError(permission string, err error) *credentialScopeError {
	scope := zentinelleScopeForPermission(permission)
	hint := fmt.Sprintf("This credential's scopes do not allow %s, and your account's roles cannot widen them, whatever astro perms diagnose shows. "+
		"The default and --scope clusters sign-ins carry no Zentinelle permission; astro auth login --scope %s does on a server that supports it. "+
		"An API token works when its own scopes allow %s.", permission, scope, permission)
	return &credentialScopeError{
		advice: credentialAdvice{Permission: permission, LoginScope: scope, Hint: hint},
		err:    err,
	}
}

// zentinelleCredentialError adds the sign-in advice when the server refused a
// Zentinelle permission for the credential's scopes. Every other error, an
// account refusal included, passes through unchanged.
func zentinelleCredentialError(err error) error {
	var failure *api.GraphQLResponseError
	if !errors.As(err, &failure) {
		return err
	}
	for _, item := range failure.Errors {
		if item.Code == "PERMISSION_DENIED" && item.Reason == tokenScopeRefusal && strings.HasPrefix(item.Permission, "zentinelle.") {
			return newCredentialScopeError(item.Permission, err)
		}
	}
	return err
}

// zentinelleReadErrors wraps a Zentinelle read: a scope refusal gets the
// sign-in advice, and under --json any error is one JSON object on stderr.
func zentinelleReadErrors(run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		return permissionCommandError(cmd, zentinelleCredentialError(run(cmd, args)))
	}
}
