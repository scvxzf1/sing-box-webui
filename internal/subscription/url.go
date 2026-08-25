package subscription

import (
	"errors"
	"net/url"
	"strings"
)

func redactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	if parsed.RawQuery != "" {
		parsed.RawQuery = "redacted"
	}
	parsed.Fragment = ""
	return parsed.String()
}

func redactURLError(err error) error {
	var urlError *url.Error
	if !errors.As(err, &urlError) {
		return err
	}
	sanitized := *urlError
	sanitized.URL = redactURL(urlError.URL)
	return &sanitized
}

func redactURLInText(message, rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return message
	}
	redacted := redactURL(rawURL)
	if redacted == "" {
		redacted = "[redacted subscription URL]"
	}
	return strings.ReplaceAll(message, rawURL, redacted)
}

type redactedError struct {
	message string
	cause   error
}

func (e redactedError) Error() string { return e.message }
func (e redactedError) Unwrap() error { return e.cause }

func redactError(err error, rawURL string) error {
	message := redactURLInText(err.Error(), rawURL)
	if message == err.Error() {
		return err
	}
	return redactedError{message: message, cause: err}
}
