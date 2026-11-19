package core

import "strings"

// QueryMode controls whether context filtering applies during a query.
type QueryMode int

const (
	QueryContextual QueryMode = iota
	QueryNonContextual
)

// QueryFlags mirrors net.luckperms.api.query.Flag.
type QueryFlags struct {
	ResolveInheritance                        bool
	IncludeNodesWithoutServerContext          bool
	IncludeNodesWithoutWorldContext           bool
	ApplyInheritanceNodesWithoutServerContext bool
	ApplyInheritanceNodesWithoutWorldContext  bool
}

// DefaultQueryFlags returns all flags enabled (LuckPerms default).
func DefaultQueryFlags() QueryFlags {
	return QueryFlags{
		ResolveInheritance:                        true,
		IncludeNodesWithoutServerContext:          true,
		IncludeNodesWithoutWorldContext:           true,
		ApplyInheritanceNodesWithoutServerContext: true,
		ApplyInheritanceNodesWithoutWorldContext:  true,
	}
}

// QueryOptions specifies contexts, mode, and flags for permission/meta lookups.
type QueryOptions struct {
	Mode               QueryMode
	Contexts           ContextSet
	Flags              QueryFlags
	ContextSatisfyMode ContextSatisfyMode
	Op                 bool
}

// NonContextualQueryOptions returns QueryOptions that match all contexts.
func NonContextualQueryOptions() QueryOptions {
	return QueryOptions{
		Mode:  QueryNonContextual,
		Flags: DefaultQueryFlags(),
	}
}

// NodeApplies reports whether node is active under qo.
func (qo QueryOptions) NodeApplies(node Node) bool {
	if node.HasExpired() {
		return false
	}
	if qo.Mode == QueryNonContextual {
		return true
	}
	if !node.Contexts.IsSatisfiedBy(qo.Contexts, qo.ContextSatisfyMode) {
		return false
	}
	if node.Type == NodeTypeInheritance {
		if !qo.Flags.ApplyInheritanceNodesWithoutServerContext && !node.Contexts.ContainsKey(ContextKeyServer) {
			return false
		}
		if !qo.Flags.ApplyInheritanceNodesWithoutWorldContext && !node.Contexts.ContainsKey(ContextKeyWorld) {
			return false
		}
	} else {
		if !qo.Flags.IncludeNodesWithoutServerContext && !node.Contexts.ContainsKey(ContextKeyServer) {
			return false
		}
		if !qo.Flags.IncludeNodesWithoutWorldContext && !node.Contexts.ContainsKey(ContextKeyWorld) {
			return false
		}
	}
	return true
}

// CacheKey uniquely identifies a QueryOptions value for caching.
func (qo QueryOptions) CacheKey() string {
	var sb strings.Builder
	if qo.Mode == QueryNonContextual {
		sb.WriteByte('N')
	} else {
		sb.WriteByte('C')
	}
	if qo.Flags.ResolveInheritance {
		sb.WriteByte('1')
	} else {
		sb.WriteByte('0')
	}
	if qo.Flags.IncludeNodesWithoutServerContext {
		sb.WriteByte('1')
	} else {
		sb.WriteByte('0')
	}
	if qo.Flags.IncludeNodesWithoutWorldContext {
		sb.WriteByte('1')
	} else {
		sb.WriteByte('0')
	}
	if qo.Flags.ApplyInheritanceNodesWithoutServerContext {
		sb.WriteByte('1')
	} else {
		sb.WriteByte('0')
	}
	if qo.Flags.ApplyInheritanceNodesWithoutWorldContext {
		sb.WriteByte('1')
	} else {
		sb.WriteByte('0')
	}
	if qo.ContextSatisfyMode == AllValuesPerKey {
		sb.WriteByte('A')
	} else {
		sb.WriteByte('O')
	}
	if qo.Op {
		sb.WriteByte('P')
	} else {
		sb.WriteByte('p')
	}
	sb.WriteByte('|')
	sb.WriteString(qo.Contexts.MapKey())
	return sb.String()
}

// Tristate represents TRUE, FALSE, or UNDEFINED (matching net.luckperms.api.util.Tristate).
type Tristate int8

const (
	TristateUndefined Tristate = 0
	TristateTrue      Tristate = 1
	TristateFalse     Tristate = -1
)

// TristateOf converts a bool to TristateTrue or TristateFalse.
func TristateOf(b bool) Tristate {
	if b {
		return TristateTrue
	}
	return TristateFalse
}

// AsBool returns true only for TristateTrue.
func (t Tristate) AsBool() bool { return t == TristateTrue }

// String returns "true", "false", or "undefined".
func (t Tristate) String() string {
	switch t {
	case TristateTrue:
		return "true"
	case TristateFalse:
		return "false"
	default:
		return "undefined"
	}
}

// TristateResult records the outcome of a permission check along with its cause.
type TristateResult struct {
	Result           Tristate
	Node             *Node
	Processor        string
	OverriddenResult *TristateResult
}

// UndefinedResult is a zero TristateResult.
var UndefinedResult = TristateResult{Result: TristateUndefined}

// StringResult records a meta/prefix/suffix lookup result and its source node.
type StringResult struct {
	Value string
	Has   bool
	Node  *Node
}

// IntegerResult records a weight lookup result and its source node.
type IntegerResult struct {
	Value int
	Has   bool
	Node  *Node
}
