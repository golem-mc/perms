package core

import (
	"fmt"
	"regexp"
	"strings"
)

// BulkDataType selects which holders a bulkupdate targets.
type BulkDataType string

const (
	BulkDataAll    BulkDataType = "ALL"
	BulkDataUsers  BulkDataType = "USERS"
	BulkDataGroups BulkDataType = "GROUPS"
)

// BulkField selects which node attribute is matched or updated.
type BulkField string

const (
	BulkFieldPermission BulkField = "permission"
	BulkFieldServer     BulkField = "server"
	BulkFieldWorld      BulkField = "world"
)

// BulkComparison selects the filter comparison operator.
type BulkComparison string

const (
	BulkEq      BulkComparison = "=="
	BulkNeq     BulkComparison = "!="
	BulkLike    BulkComparison = "~~"
	BulkNotLike BulkComparison = "!~"
)

// BulkConstraint filters nodes during a BulkUpdate.
type BulkConstraint struct {
	Field      BulkField
	Comparison BulkComparison
	Value      string
}

// BulkActionType selects delete vs update.
type BulkActionType string

const (
	BulkActionDelete BulkActionType = "delete"
	BulkActionUpdate BulkActionType = "update"
)

// BulkUpdateStats tracks how many nodes and holders a BulkUpdate modified.
type BulkUpdateStats struct {
	AffectedNodes  int
	AffectedUsers  int
	AffectedGroups int
}

// BulkUpdate represents a bulk database mutation matching LuckPerms `/lp bulkupdate`.
type BulkUpdate struct {
	DataType        BulkDataType
	Action          BulkActionType
	UpdateField     BulkField
	UpdateValue     string
	Constraints     []BulkConstraint
	TrackStatistics bool
	Stats           BulkUpdateStats
}

// ParseBulkField parses a field name.
func ParseBulkField(s string) (BulkField, bool) {
	switch strings.ToLower(s) {
	case "permission":
		return BulkFieldPermission, true
	case "server":
		return BulkFieldServer, true
	case "world":
		return BulkFieldWorld, true
	default:
		return "", false
	}
}

// ParseBulkComparison parses a comparison operator.
func ParseBulkComparison(s string) (BulkComparison, bool) {
	switch s {
	case "==":
		return BulkEq, true
	case "!=":
		return BulkNeq, true
	case "~~":
		return BulkLike, true
	case "!~":
		return BulkNotLike, true
	default:
		return "", false
	}
}

// ReadableSummary formats a SQL-like preview for confirmation prompts.
func (bu *BulkUpdate) ReadableSummary() string {
	var sb strings.Builder
	if bu.Action == BulkActionDelete {
		sb.WriteString("DELETE FROM ")
		sb.WriteString(strings.ToLower(string(bu.DataType)))
	} else {
		fmt.Fprintf(&sb, "UPDATE %s SET %s = '%s'", strings.ToLower(string(bu.DataType)), bu.UpdateField, bu.UpdateValue)
	}
	if len(bu.Constraints) > 0 {
		sb.WriteString(" WHERE ")
		for i, c := range bu.Constraints {
			if i > 0 {
				sb.WriteString(" AND ")
			}
			fmt.Fprintf(&sb, "%s %s '%s'", c.Field, c.Comparison, c.Value)
		}
	}
	return sb.String()
}

// Matches reports whether node satisfies all constraints.
func (bu *BulkUpdate) Matches(n Node) bool {
	for _, c := range bu.Constraints {
		if !c.matches(n) {
			return false
		}
	}
	return true
}

func (c BulkConstraint) matches(n Node) bool {
	var target string
	switch c.Field {
	case BulkFieldPermission:
		target = n.Key
	case BulkFieldServer:
		if v, ok := n.Contexts.AnyValue(ContextKeyServer); ok {
			target = v
		} else {
			target = GlobalContextValue
		}
	case BulkFieldWorld:
		if v, ok := n.Contexts.AnyValue(ContextKeyWorld); ok {
			target = v
		} else {
			target = GlobalContextValue
		}
	}
	return compareBulkValue(target, c.Comparison, c.Value)
}

func compareBulkValue(target string, cmp BulkComparison, expr string) bool {
	switch cmp {
	case BulkEq:
		return strings.EqualFold(target, expr)
	case BulkNeq:
		return !strings.EqualFold(target, expr)
	case BulkLike:
		return sqlLikeMatch(target, expr)
	case BulkNotLike:
		return !sqlLikeMatch(target, expr)
	default:
		return false
	}
}

// sqlLikeMatch implements SQL LIKE matching (% = .*, _ = ., case-insensitive).
func sqlLikeMatch(target, pattern string) bool {
	var sb strings.Builder
	sb.WriteString("(?i)^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '%':
			sb.WriteString(".*")
		case '_':
			sb.WriteByte('.')
		default:
			sb.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	sb.WriteByte('$')
	rx, err := regexp.Compile(sb.String())
	if err != nil {
		return false
	}
	return rx.MatchString(target)
}

// Apply transforms a holder's nodes according to bu and updates stats.
func (bu *BulkUpdate) Apply(nodes []Node, holderType HolderType) ([]Node, bool) {
	changed := false
	affectedHere := 0
	out := make([]Node, 0, len(nodes))

	for _, n := range nodes {
		if !bu.Matches(n) {
			out = append(out, n)
			continue
		}
		changed = true
		affectedHere++
		if bu.Action == BulkActionDelete {
			continue
		}
		// Update action
		switch bu.UpdateField {
		case BulkFieldPermission:
			updated := ParseNode(bu.UpdateValue, n.Value).WithExpiry(n.Expiry).WithContexts(n.Contexts)
			out = append(out, updated)
		case BulkFieldServer:
			m := n.Contexts.MutableCopy()
			m.RemoveAll(ContextKeyServer)
			m.Add(ContextKeyServer, bu.UpdateValue)
			out = append(out, n.WithContexts(m.Immutable()))
		case BulkFieldWorld:
			m := n.Contexts.MutableCopy()
			m.RemoveAll(ContextKeyWorld)
			m.Add(ContextKeyWorld, bu.UpdateValue)
			out = append(out, n.WithContexts(m.Immutable()))
		default:
			out = append(out, n)
		}
	}

	if changed && bu.TrackStatistics {
		bu.Stats.AffectedNodes += affectedHere
		if holderType == HolderUser {
			bu.Stats.AffectedUsers++
		} else {
			bu.Stats.AffectedGroups++
		}
	}
	return out, changed
}
