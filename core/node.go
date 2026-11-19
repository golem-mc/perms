package core

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// NodeType identifies the kind of LuckPerms node.
type NodeType string

const (
	NodeTypePermission      NodeType = "permission"
	NodeTypeRegexPermission NodeType = "regex_permission"
	NodeTypeInheritance     NodeType = "inheritance"
	NodeTypePrefix          NodeType = "prefix"
	NodeTypeSuffix          NodeType = "suffix"
	NodeTypeMeta            NodeType = "meta"
	NodeTypeWeight          NodeType = "weight"
	NodeTypeDisplayName     NodeType = "display_name"
)

// HolderType identifies whether a PermissionHolder is a user or group.
type HolderType string

const (
	HolderUser  HolderType = "user"
	HolderGroup HolderType = "group"
)

// NodeOrigin records which holder a node was inherited from.
type NodeOrigin struct {
	Type HolderType
	Name string
}

// NodeEquality selects which fields are compared when checking node equality.
type NodeEquality int

const (
	// EqualityExact matches key, value, expiry, and contexts.
	EqualityExact NodeEquality = iota
	// EqualityIgnoreValue matches key, expiry, and contexts.
	EqualityIgnoreValue
	// EqualityIgnoreExpiry matches key, value, and contexts.
	EqualityIgnoreExpiry
	// EqualityIgnoreExpiryAndValue matches key and contexts.
	EqualityIgnoreExpiryAndValue
	// EqualityOnlyKey matches only key (case-insensitive).
	EqualityOnlyKey
)

// TemporaryNodeMergeStrategy controls what happens when setting a temporary node
// that already exists.
type TemporaryNodeMergeStrategy string

const (
	TempMergeDeny       TemporaryNodeMergeStrategy = "deny"
	TempMergeAccumulate TemporaryNodeMergeStrategy = "accumulate"
	TempMergeReplace    TemporaryNodeMergeStrategy = "replace"
)

// Node is a LuckPerms permission, inheritance, prefix, suffix, meta, weight, or displayname node.
type Node struct {
	Type     NodeType
	Key      string
	Value    bool
	Expiry   time.Time // zero means permanent
	Contexts ContextSet

	// Typed fields parsed from Key.
	GroupName   string // NodeTypeInheritance
	Priority    int    // NodeTypePrefix, NodeTypeSuffix
	MetaKey     string // NodeTypeMeta
	MetaValue   string // NodeTypePrefix, NodeTypeSuffix, NodeTypeMeta
	Weight      int    // NodeTypeWeight
	DisplayName string // NodeTypeDisplayName
	Pattern     string // NodeTypeRegexPermission

	// Origin is populated during inheritance resolution (not persisted).
	Origin NodeOrigin
}

// HasExpiry reports whether the node is temporary.
func (n Node) HasExpiry() bool { return !n.Expiry.IsZero() }

// HasExpired reports whether the node has expired at now.
func (n Node) HasExpired() bool {
	return !n.Expiry.IsZero() && !time.Now().Before(n.Expiry)
}

// Equal compares two nodes under the given predicate.
func (n Node) Equal(other Node, eq NodeEquality) bool {
	if !strings.EqualFold(n.Key, other.Key) {
		return false
	}
	if eq == EqualityOnlyKey {
		return true
	}
	if !n.Contexts.Equal(other.Contexts) {
		return false
	}
	switch eq {
	case EqualityIgnoreExpiryAndValue:
		return true
	case EqualityIgnoreValue:
		return n.Expiry.Unix() == other.Expiry.Unix() && n.HasExpiry() == other.HasExpiry()
	case EqualityIgnoreExpiry:
		return n.Value == other.Value
	default: // EqualityExact
		return n.Value == other.Value && n.Expiry.Unix() == other.Expiry.Unix() && n.HasExpiry() == other.HasExpiry()
	}
}

// WithValue returns a copy with Value set.
func (n Node) WithValue(v bool) Node { n.Value = v; return n }

// WithExpiry returns a copy with Expiry set (truncated to whole seconds like LuckPerms).
func (n Node) WithExpiry(t time.Time) Node {
	if t.IsZero() {
		n.Expiry = time.Time{}
	} else {
		n.Expiry = t.UTC().Truncate(time.Second)
	}
	return n
}

// WithContexts returns a copy with Contexts set.
func (n Node) WithContexts(cs ContextSet) Node { n.Contexts = cs; return n }

// WithOrigin returns a copy with Origin set.
func (n Node) WithOrigin(origin NodeOrigin) Node { n.Origin = origin; return n }

// IsWildcard reports whether n is a wildcard permission node ("*" or "foo.*").
func (n Node) IsWildcard() bool {
	if n.Type != NodeTypePermission {
		return false
	}
	return IsWildcardKey(n.Key)
}

// IsWildcardKey reports whether key is "*" / "'*'" or ends with ".*".
func IsWildcardKey(key string) bool {
	return key == "*" || key == "'*'" || (len(key) > 2 && strings.HasSuffix(key, ".*"))
}

// EscapeDelimiters escapes '.' with '\.' matching LuckPerms Delimiters.escapeCharacters.
func EscapeDelimiters(s string) string {
	if !strings.ContainsRune(s, '.') {
		return s
	}
	return strings.ReplaceAll(s, ".", `\.`)
}

// UnescapeDelimiters reverses LuckPerms Delimiters.unescapeCharacters (including legacy \/, \-, \$).
func UnescapeDelimiters(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			next := s[i+1]
			if next == '.' || next == '/' || next == '-' || next == '$' {
				sb.WriteByte(next)
				i++
				continue
			}
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// splitNodeSeparatorInTwo splits s on the first unescaped '.'.
func splitNodeSeparatorInTwo(s string) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && (i == 0 || s[i-1] != '\\') {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}

// NewPermissionNode constructs a regular permission node.
func NewPermissionNode(perm string, value bool) Node {
	return Node{
		Type:  NodeTypePermission,
		Key:   perm,
		Value: value,
	}
}

// NewRegexNode constructs a regex permission node (key "r=<pattern>").
func NewRegexNode(pattern string, value bool) Node {
	return Node{
		Type:    NodeTypeRegexPermission,
		Key:     "r=" + pattern,
		Value:   value,
		Pattern: pattern,
	}
}

// NewInheritanceNode constructs a group inheritance node (key "group.<name>").
func NewInheritanceNode(group string) Node {
	g := strings.ToLower(group)
	return Node{
		Type:      NodeTypeInheritance,
		Key:       "group." + g,
		Value:     true,
		GroupName: g,
	}
}

// NewPrefixNode constructs a prefix node (key "prefix.<priority>.<escaped>").
func NewPrefixNode(priority int, prefix string) Node {
	return Node{
		Type:      NodeTypePrefix,
		Key:       "prefix." + strconv.Itoa(priority) + "." + EscapeDelimiters(prefix),
		Value:     true,
		Priority:  priority,
		MetaValue: prefix,
	}
}

// NewSuffixNode constructs a suffix node (key "suffix.<priority>.<escaped>").
func NewSuffixNode(priority int, suffix string) Node {
	return Node{
		Type:      NodeTypeSuffix,
		Key:       "suffix." + strconv.Itoa(priority) + "." + EscapeDelimiters(suffix),
		Value:     true,
		Priority:  priority,
		MetaValue: suffix,
	}
}

// NewMetaNode constructs a meta node (key "meta.<escaped-key>.<escaped-val>").
func NewMetaNode(metaKey, metaValue string) Node {
	mk := strings.ToLower(metaKey)
	return Node{
		Type:      NodeTypeMeta,
		Key:       "meta." + EscapeDelimiters(mk) + "." + EscapeDelimiters(metaValue),
		Value:     true,
		MetaKey:   mk,
		MetaValue: metaValue,
	}
}

// NewWeightNode constructs a weight node (key "weight.<w>").
func NewWeightNode(weight int) Node {
	return Node{
		Type:   NodeTypeWeight,
		Key:    "weight." + strconv.Itoa(weight),
		Value:  true,
		Weight: weight,
	}
}

// NewDisplayNameNode constructs a displayname node (key "displayname.<name>").
func NewDisplayNameNode(name string) Node {
	return Node{
		Type:        NodeTypeDisplayName,
		Key:         "displayname." + name,
		Value:       true,
		DisplayName: name,
	}
}

// ParseNode determines the most applicable Node type for key (matching NodeBuilders.determineMostApplicable).
func ParseNode(key string, value bool) Node {
	lower := strings.ToLower(key)

	// 1. Inheritance: group.<name>
	if strings.HasPrefix(lower, "group.") && len(lower) > len("group.") {
		n := NewInheritanceNode(key[len("group."):])
		n.Value = value
		return n
	}

	// 2. Prefix: prefix.<priority>.<value>
	if strings.HasPrefix(lower, "prefix.") {
		rest := key[len("prefix."):]
		if prioStr, val, ok := splitNodeSeparatorInTwo(rest); ok {
			if prio, err := strconv.Atoi(prioStr); err == nil {
				n := NewPrefixNode(prio, UnescapeDelimiters(val))
				n.Value = value
				return n
			}
		}
	}

	// 3. Suffix: suffix.<priority>.<value>
	if strings.HasPrefix(lower, "suffix.") {
		rest := key[len("suffix."):]
		if prioStr, val, ok := splitNodeSeparatorInTwo(rest); ok {
			if prio, err := strconv.Atoi(prioStr); err == nil {
				n := NewSuffixNode(prio, UnescapeDelimiters(val))
				n.Value = value
				return n
			}
		}
	}

	// 4. Meta: meta.<key>.<value>
	if strings.HasPrefix(lower, "meta.") {
		rest := key[len("meta."):]
		if mk, mv, ok := splitNodeSeparatorInTwo(rest); ok && mk != "" {
			n := NewMetaNode(UnescapeDelimiters(mk), UnescapeDelimiters(mv))
			n.Value = value
			return n
		}
	}

	// 5. Weight: weight.<int>
	if strings.HasPrefix(lower, "weight.") {
		rest := key[len("weight."):]
		if w, err := strconv.Atoi(rest); err == nil {
			n := NewWeightNode(w)
			n.Value = value
			return n
		}
	}

	// 6. DisplayName: displayname.<name>
	if strings.HasPrefix(lower, "displayname.") && len(key) > len("displayname.") {
		n := NewDisplayNameNode(key[len("displayname."):])
		n.Value = value
		return n
	}

	// 7. RegexPermission: r=<pattern> or R=<pattern>
	if (strings.HasPrefix(key, "r=") || strings.HasPrefix(key, "R=")) && len(key) > 2 {
		n := NewRegexNode(key[2:], value)
		return n
	}

	// 8. Default: Permission
	return NewPermissionNode(key, value)
}

// CompareNodes compares two nodes for deterministic sorting (matching NodeWithContextComparator).
func CompareNodes(a, b Node) int {
	// Contextual nodes have higher priority than global nodes (more contexts first).
	if c := cmp.Compare(b.Contexts.Len(), a.Contexts.Len()); c != 0 {
		return c
	}
	if c := a.Contexts.Compare(b.Contexts); c != 0 {
		return c
	}
	// Temporary nodes before permanent nodes.
	if a.HasExpiry() != b.HasExpiry() {
		if a.HasExpiry() {
			return -1
		}
		return 1
	}
	if a.HasExpiry() {
		if c := cmp.Compare(a.Expiry.Unix(), b.Expiry.Unix()); c != 0 {
			return c
		}
	}
	// Non-wildcard before wildcard.
	if a.IsWildcard() != b.IsWildcard() {
		if !a.IsWildcard() {
			return -1
		}
		return 1
	}
	if c := cmp.Compare(strings.ToLower(a.Key), strings.ToLower(b.Key)); c != 0 {
		return c
	}
	if a.Value != b.Value {
		if a.Value {
			return -1
		}
		return 1
	}
	return 0
}

// --- Shorthand expansion (matches LuckPerms ShorthandParser) ---

const (
	maxShorthandRange   = 250
	maxShorthandResults = 1000
)

// ExpandShorthand expands LuckPerms shorthand patterns like "perm.{1-4}", "perm.(a|b)", "perm.{a-c}".
// Returns nil if no shorthand groups were expanded.
func ExpandShorthand(s string) ([]string, error) {
	if !strings.ContainsAny(s, "{(") {
		return nil, nil
	}
	results := map[string]struct{}{s: {}}
	for {
		work := false
		next := make(map[string]struct{}, len(results))
		for item := range results {
			expanded, err := expandOneShorthandGroup(item)
			if err != nil {
				return nil, err
			}
			if expanded != nil {
				work = true
				for _, e := range expanded {
					next[e] = struct{}{}
				}
			} else {
				next[item] = struct{}{}
			}
			if len(next) > maxShorthandResults {
				return nil, fmt.Errorf("shorthand expansion exceeded limit of %d for %q", maxShorthandResults, item)
			}
		}
		results = next
		if !work {
			break
		}
	}
	delete(results, s)
	if len(results) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(results))
	for r := range results {
		out = append(out, r)
	}
	slices.Sort(out)
	return out, nil
}

func expandOneShorthandGroup(input string) ([]string, error) {
	openIdx := strings.IndexByte(input, '{')
	if openIdx == -1 {
		openIdx = strings.IndexByte(input, '(')
	}
	if openIdx == -1 {
		return nil, nil
	}
	closeIdx := strings.IndexByte(input, '}')
	if closeIdx == -1 {
		closeIdx = strings.IndexByte(input, ')')
	}
	if closeIdx < openIdx {
		return nil, nil
	}

	before := input[:openIdx]
	between := input[openIdx+1 : closeIdx]
	after := input[closeIdx+1:]

	var parts []string
	// 1. Numeric range: a-b
	if idx := strings.IndexByte(between, '-'); idx > 0 && idx < len(between)-1 {
		a, errA := strconv.Atoi(between[:idx])
		b, errB := strconv.Atoi(between[idx+1:])
		if errA == nil && errB == nil {
			lo, hi := min(a, b), max(a, b)
			if hi-lo > maxShorthandRange {
				return nil, fmt.Errorf("range %d-%d exceeds limit of %d", a, b, maxShorthandRange)
			}
			for i := lo; i <= hi; i++ {
				parts = append(parts, strconv.Itoa(i))
			}
		}
	}
	// 2. Character range: a-z (length 3, middle '-')
	if parts == nil && len(between) == 3 && between[1] == '-' {
		a, b := int(between[0]), int(between[2])
		lo, hi := min(a, b), max(a, b)
		if hi-lo > maxShorthandRange {
			return nil, fmt.Errorf("character range %c-%c exceeds limit of %d", a, b, maxShorthandRange)
		}
		for i := lo; i <= hi; i++ {
			parts = append(parts, string(rune(i)))
		}
	}
	// 3. List: aa,bb|cc
	if parts == nil {
		if strings.ContainsAny(between, ",|") {
			for _, tok := range strings.FieldsFunc(between, func(r rune) bool { return r == ',' || r == '|' }) {
				if tok != "" {
					parts = append(parts, tok)
				}
			}
		} else {
			parts = []string{between}
		}
	}

	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, before+p+after)
	}
	return out, nil
}

// --- Duration parsing & formatting (matches LuckPerms DurationParser / DurationFormatter) ---

var durationRegex = regexp.MustCompile(
	`(?i)^\s*` +
		`(?:(\d+)\s*y(?:ear)?s?[,\s]*)?` +
		`(?:(\d+)\s*mo(?:nth)?s?[,\s]*)?` +
		`(?:(\d+)\s*w(?:eek)?s?[,\s]*)?` +
		`(?:(\d+)\s*d(?:ay)?s?[,\s]*)?` +
		`(?:(\d+)\s*h(?:our|r)?s?[,\s]*)?` +
		`(?:(\d+)\s*m(?:inute|in)?s?[,\s]*)?` +
		`(?:(\d+)\s*s(?:econd|ec)?s?[,\s]*)?$`,
)

var (
	ErrInvalidDuration = errors.New("invalid duration")
	ErrPastDate        = errors.New("date is in the past")
)

// ParseDuration parses a LuckPerms duration string (e.g. "1d12h30m") or a future unix timestamp (seconds).
func ParseDuration(input string) (time.Duration, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, ErrInvalidDuration
	}
	// Unix timestamp (in seconds)
	if ts, err := strconv.ParseInt(input, 10, 64); err == nil {
		now := time.Now().Truncate(time.Second)
		target := time.Unix(ts, 0)
		d := target.Sub(now)
		if d < 0 {
			return 0, ErrPastDate
		}
		return d, nil
	}

	m := durationRegex.FindStringSubmatch(input)
	if m == nil {
		return 0, ErrInvalidDuration
	}

	// ChronoUnit durations in Java:
	// YEAR = 31556952s (365.2425 days), MONTH = 2629746s (31556952/12), WEEK = 7d, DAY = 24h
	unitSecs := [7]int64{31556952, 2629746, 7 * 86400, 86400, 3600, 60, 1}
	var totalSecs int64
	matchedAny := false
	for i := range 7 {
		if m[i+1] != "" {
			matchedAny = true
			n, err := strconv.ParseInt(m[i+1], 10, 64)
			if err != nil {
				return 0, ErrInvalidDuration
			}
			totalSecs += n * unitSecs[i]
		}
	}
	if !matchedAny || totalSecs <= 0 {
		return 0, ErrInvalidDuration
	}
	return time.Duration(totalSecs) * time.Second, nil
}

// FormatDuration formats d concisely like LuckPerms ("1d 2h 30m 5s").
func FormatDuration(d time.Duration) string {
	secs := int64(d.Seconds())
	if secs <= 0 {
		return "0s"
	}
	type unit struct {
		name string
		secs int64
	}
	units := []unit{
		{"y", 31556952},
		{"mo", 2629746},
		{"w", 7 * 86400},
		{"d", 86400},
		{"h", 3600},
		{"m", 60},
		{"s", 1},
	}
	var parts []string
	for _, u := range units {
		if secs >= u.secs {
			n := secs / u.secs
			secs %= u.secs
			parts = append(parts, strconv.FormatInt(n, 10)+u.name)
		}
	}
	if len(parts) == 0 {
		return "0s"
	}
	return strings.Join(parts, " ")
}

// --- JSON serialization (matches LuckPerms NodeJsonSerializer for backups & JSON storage) ---

// NodeJSON is the wire/export JSON shape of a LuckPerms node.
type NodeJSON struct {
	Type    string         `json:"type,omitempty"`
	Key     string         `json:"key"`
	Value   bool           `json:"value"`
	Expiry  int64          `json:"expiry,omitempty"`
	Context map[string]any `json:"context,omitempty"`
}

// ToJSON converts n into its LuckPerms JSON representation.
func (n Node) ToJSON() NodeJSON {
	out := NodeJSON{
		Type:  string(n.Type),
		Key:   n.Key,
		Value: n.Value,
	}
	if n.HasExpiry() {
		out.Expiry = n.Expiry.Unix()
	}
	if !n.Contexts.IsEmpty() {
		out.Context = ContextSetToJSONMap(n.Contexts)
	}
	return out
}

// NodeFromJSON parses a LuckPerms NodeJSON into a Node.
func NodeFromJSON(j NodeJSON) (Node, bool) {
	if j.Key == "" {
		return Node{}, false
	}
	n := ParseNode(j.Key, j.Value)
	if j.Expiry > 0 {
		n.Expiry = time.Unix(j.Expiry, 0).UTC()
	}
	if len(j.Context) > 0 {
		n.Contexts = ContextSetFromJSONMap(j.Context)
	}
	return n, true
}

// ContextSetToJSONMap serializes a ContextSet in LuckPerms's JSON format:
// single value as string, multiple values as []string.
func ContextSetToJSONMap(cs ContextSet) map[string]any {
	if cs.IsEmpty() {
		return nil
	}
	grouped := cs.Map()
	out := make(map[string]any, len(grouped))
	for k, vals := range grouped {
		if len(vals) == 1 {
			out[k] = vals[0]
		} else {
			out[k] = vals
		}
	}
	return out
}

// ContextSetFromJSONMap deserializes a LuckPerms JSON/YAML context map.
func ContextSetFromJSONMap(m map[string]any) ContextSet {
	if len(m) == 0 {
		return EmptyContextSet
	}
	b := NewMutableContextSet()
	for k, raw := range m {
		switch v := raw.(type) {
		case string:
			b.Add(k, v)
		case []any:
			for _, elem := range v {
				b.Add(k, fmt.Sprint(elem))
			}
		case []string:
			for _, elem := range v {
				b.Add(k, elem)
			}
		default:
			if raw != nil {
				b.Add(k, fmt.Sprint(raw))
			}
		}
	}
	return b.Immutable()
}

// MarshalJSON implements json.Marshaler for Node using LuckPerms's format.
func (n Node) MarshalJSON() ([]byte, error) {
	return json.Marshal(n.ToJSON())
}

// UnmarshalJSON implements json.Unmarshaler for Node.
func (n *Node) UnmarshalJSON(data []byte) error {
	var j NodeJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return err
	}
	parsed, ok := NodeFromJSON(j)
	if !ok {
		return errors.New("empty node key")
	}
	*n = parsed
	return nil
}
