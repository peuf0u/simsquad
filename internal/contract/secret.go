package contract

import (
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// SecretMask is the value a secret context key shows in everything simsquad
// prints or writes into a run: the key stays, so consumers still see which
// credentials the squad carries, and context stays a map[string]string.
const SecretMask = "<secret>"

// secretNameParts mark a context key secret by its name alone, so a
// password put in plain [context] (or a record written before secrets
// existed) is masked without the user marking it. There is no opt-out: a
// key that isn't a secret gets a different name.
var secretNameParts = []string{"password", "secret", "token"}

// SecretByName reports whether key's name marks it secret: it contains
// password, secret or token, in any case.
func SecretByName(key string) bool {
	lower := strings.ToLower(key)
	for _, part := range secretNameParts {
		if strings.Contains(lower, part) {
			return true
		}
	}
	return false
}

// IsSecret is the one rule for "is this context key a secret": it is in the
// explicit set (keys from [secrets], --secret or --set-secret) or its name
// marks it.
func IsSecret(key string, explicit []string) bool {
	return slices.Contains(explicit, key) || SecretByName(key)
}

// MaskContext returns a copy of ctx with every secret's value replaced by
// SecretMask; nil when ctx is empty. ctx itself is not changed.
func MaskContext(ctx map[string]string, explicit []string) map[string]string {
	if len(ctx) == 0 {
		return nil
	}
	out := make(map[string]string, len(ctx))
	for k, v := range ctx {
		if IsSecret(k, explicit) {
			v = SecretMask
		}
		out[k] = v
	}
	return out
}

// IsSecret reports whether the squad's context key is a secret.
func (s SquadRecord) IsSecret(key string) bool { return IsSecret(key, s.SecretKeys) }

// MaskedContext is the squad's context as every output shows it.
func (s SquadRecord) MaskedContext() map[string]string {
	return MaskContext(s.Context, s.SecretKeys)
}

// DeviceView projects one of the squad's devices with the masked context.
// It is what every output path uses; RevealedDeviceView is the exception.
func (s SquadRecord) DeviceView(d Device) DeviceView {
	return NewDeviceView(d, s.CreatedAt, s.MaskedContext())
}

// RevealedDeviceView projects a device with the real context values. Only
// `devices --name … --reveal` prints it: a worker fetches a secret there to
// type it into the app.
func (s SquadRecord) RevealedDeviceView(d Device) DeviceView {
	return NewDeviceView(d, s.CreatedAt, s.Context)
}

// minNameRuleRedact is the shortest value redacted from worker.log when
// only its key's name marks it secret. Such keys are often settings, not
// credentials (TOKEN_TTL=5, PASSWORD_MIN=8): redacting every "5" would
// wreck the log and hide a "5-hour limit reached" from the usage-limit
// check. Boolean values (SECRET_MENU=true, ENABLE_PASSWORD_RESET=false)
// are settings too and are left out whatever their length. Explicit
// secrets are redacted whatever their value, and outputs mask every secret
// regardless.
const minNameRuleRedact = 4

// SecretValues returns the values to redact from a worker's log: every
// non-empty explicit secret, and each value secret only by its key's name
// that has at least minNameRuleRedact characters and isn't true or false.
// Longest first, so a redactor replacing them in order never leaves part
// of a longer secret that contains a shorter one.
func (s SquadRecord) SecretValues() []string {
	var out []string
	for k, v := range s.Context {
		if v == "" || slices.Contains(out, v) {
			continue
		}
		explicit := slices.Contains(s.SecretKeys, k)
		if explicit || (SecretByName(k) && redactByName(v)) {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// redactByName reports whether a value secret only by its key's name is
// redacted from worker.log (see minNameRuleRedact).
func redactByName(v string) bool {
	if strings.EqualFold(v, "true") || strings.EqualFold(v, "false") {
		return false
	}
	return utf8.RuneCountInString(v) >= minNameRuleRedact
}

// SetContext sets a context key. secret marks it secret; a key that is
// already a secret stays one, so re-setting a password through a plain
// source can't make it printable. Only removing the key un-marks it.
func (s *SquadRecord) SetContext(key, value string, secret bool) {
	if s.Context == nil {
		s.Context = map[string]string{}
	}
	s.Context[key] = value
	if secret && !slices.Contains(s.SecretKeys, key) {
		s.SecretKeys = append(s.SecretKeys, key)
		slices.Sort(s.SecretKeys)
	}
}

// UnsetContext removes a context key and its secret mark.
func (s *SquadRecord) UnsetContext(key string) {
	delete(s.Context, key)
	s.SecretKeys = slices.DeleteFunc(s.SecretKeys, func(k string) bool { return k == key })
	if len(s.Context) == 0 {
		s.Context = nil // honour omitempty
	}
	if len(s.SecretKeys) == 0 {
		s.SecretKeys = nil
	}
}

// ClearContext removes every context key and secret mark.
func (s *SquadRecord) ClearContext() {
	s.Context = nil
	s.SecretKeys = nil
}
