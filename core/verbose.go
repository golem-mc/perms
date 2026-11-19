package core

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golem-mc/golem/plugin"
)

// CheckOrigin describes what initiated a permission or meta check.
type CheckOrigin string

const (
	OriginPlatformPermissionCheck CheckOrigin = "PLATFORM_PERMISSION_CHECK"
	OriginLuckPermsAPI            CheckOrigin = "LUCKPERMS_API"
	OriginInternal                CheckOrigin = "INTERNAL"
)

// VerboseEvent records one permission check for live verbose output or recording.
type VerboseEvent struct {
	Timestamp  time.Time
	Target     string
	TargetType HolderType
	Contexts   ContextSet
	Permission string
	Result     TristateResult
	Origin     CheckOrigin
}

// VerboseFilter evaluates boolean filter expressions like "(Alice & !minecraft.*) | essentials.home".
type VerboseFilter struct {
	raw  string
	eval func(ev VerboseEvent) bool
}

// CompileVerboseFilter compiles a LuckPerms verbose filter string.
// Empty string matches everything.
func CompileVerboseFilter(expr string) (*VerboseFilter, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return &VerboseFilter{raw: "", eval: func(VerboseEvent) bool { return true }}, nil
	}
	tokens, err := tokenizeVerboseExpr(expr)
	if err != nil {
		return nil, err
	}
	p := &verboseParser{tokens: tokens}
	fn, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.tokens) {
		return nil, fmt.Errorf("unexpected token %q in verbose filter", p.tokens[p.pos])
	}
	return &VerboseFilter{raw: expr, eval: fn}, nil
}

// String returns the original filter expression.
func (f *VerboseFilter) String() string {
	if f == nil || f.raw == "" {
		return "any"
	}
	return f.raw
}

// Matches evaluates the filter against ev.
func (f *VerboseFilter) Matches(ev VerboseEvent) bool {
	if f == nil || f.eval == nil {
		return true
	}
	return f.eval(ev)
}

func tokenizeVerboseExpr(s string) ([]string, error) {
	var tokens []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case ' ', '\t':
			flush()
		case '(', ')', '!', '&', '|':
			flush()
			// Handle && or || as single operator
			if (c == '&' || c == '|') && i+1 < len(s) && s[i+1] == c {
				i++
			}
			tokens = append(tokens, string(c))
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	if len(tokens) == 0 {
		return nil, errors.New("empty filter expression")
	}
	return tokens, nil
}

type verboseParser struct {
	tokens []string
	pos    int
}

func (p *verboseParser) parseOr() (func(VerboseEvent) bool, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.pos < len(p.tokens) && p.tokens[p.pos] == "|" {
		p.pos++
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(ev VerboseEvent) bool { return l(ev) || r(ev) }
	}
	return left, nil
}

func (p *verboseParser) parseAnd() (func(VerboseEvent) bool, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.pos < len(p.tokens) {
		tok := p.tokens[p.pos]
		if tok == "&" {
			p.pos++
		} else if tok == "|" || tok == ")" {
			break
		}
		// Implicit OR/AND: in LuckPerms space-separated terms without operator act as OR?
		// Actually in LuckPerms VerboseFilter, users write "Alice & essentials" or "Alice | Bob".
		// Treating adjacent terms as AND is standard if no operator given.
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l, r := left, right
		left = func(ev VerboseEvent) bool { return l(ev) && r(ev) }
	}
	return left, nil
}

func (p *verboseParser) parseUnary() (func(VerboseEvent) bool, error) {
	if p.pos >= len(p.tokens) {
		return nil, errors.New("unexpected end of filter expression")
	}
	if p.tokens[p.pos] == "!" {
		p.pos++
		inner, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return func(ev VerboseEvent) bool { return !inner(ev) }, nil
	}
	if p.tokens[p.pos] == "(" {
		p.pos++
		inner, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if p.pos >= len(p.tokens) || p.tokens[p.pos] != ")" {
			return nil, errors.New("missing closing ')' in filter expression")
		}
		p.pos++
		return inner, nil
	}
	term := strings.ToLower(p.tokens[p.pos])
	p.pos++
	return func(ev VerboseEvent) bool {
		if strings.EqualFold(ev.Target, term) {
			return true
		}
		if strings.HasPrefix(strings.ToLower(ev.Permission), term) {
			return true
		}
		if strings.EqualFold(ev.Result.Result.String(), term) {
			return true
		}
		if strings.EqualFold(string(ev.Origin), term) {
			return true
		}
		return false
	}, nil
}

// VerboseListener receives permission checks for a single sender.
type VerboseListener struct {
	Sender  plugin.CommandSender
	Filter  *VerboseFilter
	Notify  bool // live chat output (on) vs silent recording (record)
	Started time.Time

	mu       sync.Mutex
	recorded []VerboseEvent
	matched  int
	total    int
}

// Offer processes an event against this listener.
func (vl *VerboseListener) Offer(ev VerboseEvent) {
	vl.mu.Lock()
	vl.total++
	if !vl.Filter.Matches(ev) {
		vl.mu.Unlock()
		return
	}
	vl.matched++
	if len(vl.recorded) < 10000 {
		vl.recorded = append(vl.recorded, ev)
	}
	notify := vl.Notify
	sender := vl.Sender
	vl.mu.Unlock()

	if notify && sender != nil {
		if p, ok := sender.(plugin.Player); ok && !p.IsValid() {
			return
		}
		sender.SendMessage(formatVerboseLine(ev))
	}
}

// Snapshot returns recorded events and counters.
func (vl *VerboseListener) Snapshot() (events []VerboseEvent, matched, total int) {
	vl.mu.Lock()
	defer vl.mu.Unlock()
	out := make([]VerboseEvent, len(vl.recorded))
	copy(out, vl.recorded)
	return out, vl.matched, vl.total
}

func formatVerboseLine(ev VerboseEvent) plugin.Text {
	resText := FormatTristate(ev.Result.Result)
	hoverLines := fmt.Sprintf("Origin: %s\nContexts: %s", ev.Origin, ev.Contexts.String())
	if ev.Result.Processor != "" {
		hoverLines += "\nProcessor: " + ev.Result.Processor
	}
	if ev.Result.Node != nil {
		hoverLines += "\nCause: " + ev.Result.Node.Key
		if ev.Result.Node.Origin.Name != "" {
			hoverLines += fmt.Sprintf(" (%s:%s)", ev.Result.Node.Origin.Type, ev.Result.Node.Origin.Name)
		}
	}
	return LPPrefix().Append(
		plugin.Plain("VB > ").Color("dark_aqua").Bold(),
		plugin.Plain(ev.Target).Color("aqua"),
		plugin.Plain(" - ").Color("gray"),
		plugin.Plain(ev.Permission).Color("white").OnHover(plugin.Plain(hoverLines)),
		plugin.Plain(" - ").Color("gray"),
		resText,
	)
}

// VerboseHandler manages active verbose listeners.
type VerboseHandler struct {
	mu        sync.RWMutex
	listeners map[string]*VerboseListener // keyed by sender key ("console" or UUID)
}

func newVerboseHandler() *VerboseHandler {
	return &VerboseHandler{listeners: make(map[string]*VerboseListener)}
}

func verboseSenderKey(s plugin.CommandSender) string {
	if p, ok := s.(plugin.Player); ok {
		return p.UUID().String()
	}
	return "console"
}

// IsListening reports whether any verbose listeners are currently active.
func (vh *VerboseHandler) IsListening() bool {
	vh.mu.RLock()
	n := len(vh.listeners)
	vh.mu.RUnlock()
	return n > 0
}

// Register registers or replaces a listener for sender.
func (vh *VerboseHandler) Register(sender plugin.CommandSender, filter *VerboseFilter, notify bool) *VerboseListener {
	vl := &VerboseListener{
		Sender:  sender,
		Filter:  filter,
		Notify:  notify,
		Started: time.Now(),
	}
	vh.mu.Lock()
	vh.listeners[verboseSenderKey(sender)] = vl
	vh.mu.Unlock()
	return vl
}

// Unregister removes and returns sender's active listener, if any.
func (vh *VerboseHandler) Unregister(sender plugin.CommandSender) *VerboseListener {
	key := verboseSenderKey(sender)
	vh.mu.Lock()
	vl := vh.listeners[key]
	delete(vh.listeners, key)
	vh.mu.Unlock()
	return vl
}

// Get returns sender's active listener, if any.
func (vh *VerboseHandler) Get(sender plugin.CommandSender) *VerboseListener {
	vh.mu.RLock()
	defer vh.mu.RUnlock()
	return vh.listeners[verboseSenderKey(sender)]
}

// OfferPermissionCheck dispatches a permission check to all active verbose listeners.
func (vh *VerboseHandler) OfferPermissionCheck(target string, targetType HolderType, contexts ContextSet, perm string, res TristateResult, origin CheckOrigin) {
	vh.mu.RLock()
	if len(vh.listeners) == 0 {
		vh.mu.RUnlock()
		return
	}
	list := make([]*VerboseListener, 0, len(vh.listeners))
	for _, vl := range vh.listeners {
		list = append(list, vl)
	}
	vh.mu.RUnlock()

	ev := VerboseEvent{
		Timestamp:  time.Now(),
		Target:     target,
		TargetType: targetType,
		Contexts:   contexts,
		Permission: perm,
		Result:     res,
		Origin:     origin,
	}
	for _, vl := range list {
		vl.Offer(ev)
	}
}
