package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const authenticatorPath = "/app/auth1/authenticator"

type authenticatorReply struct {
	Available     bool     `json:"available"`
	Required      bool     `json:"required"`
	Enabled       bool     `json:"enabled"`
	EnrollmentID  string   `json:"enrollmentId,omitempty"`
	Secret        string   `json:"secret,omitempty"`
	URI           string   `json:"uri,omitempty"`
	RecoveryCodes []string `json:"recoveryCodes,omitempty"`
}

func readAuthenticatorProof(cmd *cobra.Command, action string) (password, code string, err error) {
	needsCode := action != "begin"
	if boolFlag(cmd, "proof-stdin") {
		scanner := bufio.NewScanner(cmd.InOrStdin())
		if scanner.Scan() {
			password = scanner.Text()
		}
		if needsCode && scanner.Scan() {
			code = strings.TrimSpace(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return "", "", fmt.Errorf("read authenticator credentials: %w", err)
		}
	} else {
		fd := int(os.Stdin.Fd())
		if boolFlag(cmd, "no-prompt") || !term.IsTerminal(fd) {
			return "", "", fmt.Errorf("use --proof-stdin or enter the credentials in a terminal")
		}
		read := func(prompt string) (string, error) {
			fmt.Fprint(cmd.ErrOrStderr(), prompt)
			value, err := term.ReadPassword(fd)
			fmt.Fprintln(cmd.ErrOrStderr())
			return string(value), err
		}
		password, err = read("Current password: ")
		if err != nil {
			return "", "", err
		}
		if needsCode {
			prompt := "Authenticator or recovery code: "
			if action == "confirm" || action == "confirm_replacement" {
				prompt = "Code from your new authenticator app: "
			}
			code, err = read(prompt)
			if err != nil {
				return "", "", err
			}
			code = strings.TrimSpace(code)
		}
	}

	if password == "" || (needsCode && code == "") {
		return "", "", fmt.Errorf("enter your current password and, when requested, a code")
	}
	return password, code, nil
}

func runAuthenticator(cmd *cobra.Command, action string, args []string) error {
	var payload map[string]string
	if action != "status" {
		password, code, err := readAuthenticatorProof(cmd, action)
		if err != nil {
			return err
		}
		payload = map[string]string{"action": action, "password": password, "code": code}
		if len(args) > 0 {
			payload["enrollmentId"] = args[0]
		}
	}

	client, _, _, err := loadActiveClient(cmd.Context(), boolFlag(cmd, "debug"))
	if err != nil {
		return err
	}
	var reply authenticatorReply
	if payload == nil {
		err = client.Get(cmd.Context(), authenticatorPath, &reply)
	} else {
		err = client.Post(cmd.Context(), authenticatorPath, payload, &reply)
	}
	if err != nil {
		return err
	}
	if boolFlag(cmd, "json") {
		return renderJSON(cmd, reply)
	}

	out := cmd.OutOrStdout()
	switch action {
	case "begin":
		fmt.Fprintf(out, "Setup key: %s\nEnter it in your authenticator app, then run astro auth authenticator confirm.\n", reply.Secret)
	case "begin_replacement":
		if reply.Secret == "" || reply.EnrollmentID == "" {
			return fmt.Errorf("the server did not return replacement enrollment details")
		}
		fmt.Fprintf(out, "Setup key: %s\nEnter it in your new authenticator app, then run:\n  astro auth authenticator confirm-replacement %s\n", reply.Secret, reply.EnrollmentID)
	case "confirm", "confirm_replacement", "regenerate":
		fmt.Fprintln(out, "Save these one-time recovery codes now:")
		for _, code := range reply.RecoveryCodes {
			fmt.Fprintln(out, code)
		}
	case "remove":
		fmt.Fprintln(out, "Authenticator removed.")
	case "status":
		switch {
		case reply.Enabled:
			fmt.Fprintln(out, "Authenticator enabled for password sign-in.")
		case reply.Available && reply.Required:
			fmt.Fprintln(out, "Authenticator enrollment is required for password sign-in.")
		case reply.Available:
			fmt.Fprintln(out, "Authenticator available for password sign-in.")
		default:
			fmt.Fprintln(out, "This account has no local password.")
		}
	}
	return nil
}

func newAuthenticatorCommand() *cobra.Command {
	parent := &cobra.Command{Use: "authenticator", Short: "Manage your authenticator app for password sign-in"}
	for _, action := range []struct {
		use, help, api string
		args           cobra.PositionalArgs
	}{
		{"status", "Show your authenticator status", "status", cobra.NoArgs},
		{"begin", "Generate an authenticator setup key", "begin", cobra.NoArgs},
		{"confirm [enrollment-id]", "Verify the setup code and enable two-factor authentication", "confirm", cobra.MaximumNArgs(1)},
		{"replace", "Prepare a new authenticator using a current code", "begin_replacement", cobra.NoArgs},
		{"confirm-replacement <enrollment-id>", "Verify and activate your new authenticator", "confirm_replacement", cobra.ExactArgs(1)},
		{"regenerate", "Replace your recovery codes using a valid code", "regenerate", cobra.NoArgs},
		{"remove", "Remove your authenticator using a valid code", "remove", cobra.NoArgs},
	} {
		cmd := &cobra.Command{
			Use: action.use, Short: action.help, Args: action.args,
			RunE: func(cmd *cobra.Command, args []string) error {
				return runAuthenticator(cmd, action.api, args)
			},
		}
		if action.api != "status" {
			cmd.Flags().Bool("proof-stdin", false, "Read the password, then the code when required, on separate lines")
		}
		parent.AddCommand(cmd)
	}

	return parent
}

func init() {
	authCmd.AddCommand(newAuthenticatorCommand())
}
