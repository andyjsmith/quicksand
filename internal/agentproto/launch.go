package agentproto

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Validate checks that launch settings make sense: a command if anything
// is set, and the URL placeholder in OpenURL.
func (s LaunchSettings) Validate() error {
	if len(s.Command) == 0 {
		if len(s.OpenURL) > 0 {
			return errors.New("openURL needs a command")
		}
		return nil
	}
	if s.Command[0] == "" {
		return errors.New("the command's program is empty")
	}
	if len(s.OpenURL) > 0 {
		if s.OpenURL[0] == "" {
			return errors.New("openURL's program is empty")
		}
		if !slices.ContainsFunc(s.OpenURL, func(a string) bool { return strings.Contains(a, URLPlaceholder) }) {
			return fmt.Errorf("openURL must contain %s where the URL goes", URLPlaceholder)
		}
	}
	return nil
}

// Equal reports whether two sets of launch settings are the same.
func (s LaunchSettings) Equal(o LaunchSettings) bool {
	return slices.Equal(s.Command, o.Command) && slices.Equal(s.OpenURL, o.OpenURL) && s.Restart == o.Restart
}

// OpenURLCommand is OpenURL with the URL in place of the placeholder.
func (s LaunchSettings) OpenURLCommand(u string) []string {
	argv := make([]string, len(s.OpenURL))
	for i, a := range s.OpenURL {
		argv[i] = strings.ReplaceAll(a, URLPlaceholder, u)
	}
	return argv
}

// CheckOpenURL checks that a URL may be opened in an app: an absolute http
// or https URL. That also keeps it from being taken for a command-line
// option.
func CheckOpenURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q isn't an http or https URL", s)
	}
	if strings.ContainsAny(s, "\x00\r\n") {
		return fmt.Errorf("%q contains control characters", s)
	}
	return nil
}
