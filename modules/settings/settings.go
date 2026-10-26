// Package settings describes an application's settings once, so that a web
// form, a terminal UI and a command line edit them the same way: the same
// fields, defaults, ranges, validation and error messages.
//
// An application lists its fields in a Registry. The Registry then serves
// GET/PUT /settings for generic web clients (the gleann web UI renders the
// form from the description), and gives terminal UIs Value, Display, Step,
// Reset and Set, so a menu row needs no code of its own.
//
// Conventions:
//   - A field's Key is the JSON key of the config value it binds, so
//     Uncovered can report config values no interface exposes.
//   - When a field has a Default, the zero value means "use the default" and
//     saving the default stores the zero value. A later change of the
//     default then reaches users who never chose a value.
//
// The package uses the standard library only, so plugins can depend on it
// without pulling in gleann.
package settings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// SchemaVersion is the version of the GET/PUT /settings contract. Version 2
// adds the bool and secret types, allow_custom, options_error, step,
// visible_if, apply and revision to version 1; a version 1 client still
// renders a version 2 form.
const SchemaVersion = 2

// Type is the kind of input a field needs.
type Type string

const (
	Select Type = "select" // one of Options (any value when AllowCustom)
	Text   Type = "text"   // free text; Options are suggestions
	Path   Type = "path"   // a file or directory; Options are suggestions
	Number Type = "number" // Min..Max; Options, if any, are suggestions Step cycles
	Bool   Type = "bool"   // on/off
	Secret Type = "secret" // never sent to clients, only whether it is set
)

// Apply says what saving a field takes to have an effect.
type Apply string

const (
	Live    Apply = "live"    // picked up without a restart
	Restart Apply = "restart" // the service must be restarted
)

// Option is one allowed or suggested value.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Opts builds options from value, label pairs.
func Opts(pairs ...string) []Option {
	var o []Option
	for i := 0; i+1 < len(pairs); i += 2 {
		o = append(o, Option{pairs[i], pairs[i+1]})
	}
	return o
}

// Cond shows a field only while another field has a given value.
type Cond struct {
	Key    string `json:"key"`
	Equals any    `json:"equals"`
}

// F returns a pointer to f, for Min, Max and Step.
func F(f float64) *float64 { return &f }

// Field describes one setting. C is the application's config struct.
type Field[C any] struct {
	Key   string // JSON key of the bound config value
	Label string
	Type  Type
	Group string
	Help  string

	// Bind returns a pointer to the value in c: *string, *bool, *int or
	// *float64.
	Bind func(c *C) any

	// Default is the value a zero value stands for (nil: zero is a value).
	Default any

	// Options are the allowed values of a Select, or suggestions for the
	// other types. OptionsFrom adds options known only at run time (models
	// on a server); when it fails, Options remain as suggestions and the
	// error is shown next to the field.
	Options     []Option
	OptionsFrom func(c *C) ([]Option, error)
	// AllowCustom accepts a Select value that is not among the options.
	AllowCustom bool

	Min, Max, Step *float64 // Number only

	VisibleIf *Cond
	Apply     Apply // empty: the Registry's Apply

	// Placeholder describes what an empty value means right now, e.g. the
	// library an automatic lookup finds. Clients show it in the empty input.
	Placeholder func(c *C) string

	// Check validates and may normalise a value after the generic checks
	// (type, options, range). v is a string, float64 or bool.
	Check func(c *C, v any) (any, error)
}

// Registry is the complete list of an application's settings.
type Registry[C any] struct {
	Title string
	// Note is shown after a save that needs a restart.
	Note   string
	Fields []Field[C]
	// Internal lists JSON keys of C that are deliberately not settings
	// (install state, lists managed elsewhere); see Uncovered.
	Internal []string
	// Apply is the default for fields that leave Apply empty (Restart).
	Apply Apply
	// ModelsPath, when set, is the path of the application's Catalog
	// (e.g. "/models"); the form tells clients so they can offer downloads.
	ModelsPath string

	// Load and Save read and write the config; Handler needs both.
	Load func() (*C, error)
	Save func(c *C) error
	// Validate checks the whole config after a change, for rules that
	// span fields. Its error is reported under the key "".
	Validate func(c *C) error
	// BeforeSave adjusts the config before Handler saves it, e.g. to mark
	// setup as completed.
	BeforeSave func(c *C)
}

// Spec is the description of a field sent to clients.
type Spec struct {
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	Type         Type     `json:"type"`
	Group        string   `json:"group,omitempty"`
	Help         string   `json:"help,omitempty"`
	Options      []Option `json:"options,omitempty"`
	OptionsError string   `json:"options_error,omitempty"`
	AllowCustom  bool     `json:"allow_custom,omitempty"`
	Value        any      `json:"value"`
	IsSet        bool     `json:"is_set,omitempty"` // Secret only
	Placeholder  string   `json:"placeholder,omitempty"`
	Default      any      `json:"default,omitempty"`
	Min          *float64 `json:"min,omitempty"`
	Max          *float64 `json:"max,omitempty"`
	Step         *float64 `json:"step,omitempty"`
	VisibleIf    *Cond    `json:"visible_if,omitempty"`
	Apply        Apply    `json:"apply"`
}

// Form is the GET /settings response.
type Form struct {
	SchemaVersion int    `json:"schema_version"`
	Title         string `json:"title,omitempty"`
	RestartNote   string `json:"restart_note,omitempty"`
	// Revision identifies the config the form was made from; a PUT that
	// carries it is refused if the config changed in the meantime.
	Revision string `json:"revision"`
	// Models is the path of the model catalog (see Catalog), if any.
	Models string `json:"models,omitempty"`
	Fields []Spec `json:"fields"`
}

// Field returns the field with the given key.
func (r *Registry[C]) Field(key string) (*Field[C], bool) {
	for i := range r.Fields {
		if r.Fields[i].Key == key {
			return &r.Fields[i], true
		}
	}
	return nil, false
}

func (r *Registry[C]) apply(f *Field[C]) Apply {
	switch {
	case f.Apply != "":
		return f.Apply
	case r.Apply != "":
		return r.Apply
	}
	return Restart
}

// Describe returns the form for c, with current values and run-time options.
func (r *Registry[C]) Describe(c *C) Form {
	form := Form{SchemaVersion: SchemaVersion, Title: r.Title, RestartNote: r.Note, Revision: Revision(c), Models: r.ModelsPath}
	for i := range r.Fields {
		f := &r.Fields[i]
		opts, optErr := f.options(c)
		s := Spec{
			Key: f.Key, Label: f.Label, Type: f.Type, Group: f.Group, Help: f.Help,
			Options: opts, AllowCustom: f.AllowCustom, Default: f.Default,
			Min: f.Min, Max: f.Max, Step: f.Step, VisibleIf: f.VisibleIf, Apply: r.apply(f),
		}
		if optErr != nil {
			s.OptionsError = optErr.Error()
		}
		if f.Placeholder != nil && isZero(get(f.Bind(c))) {
			s.Placeholder = f.Placeholder(c)
		}
		if f.Type == Secret {
			s.Value, s.IsSet = "", !isZero(get(f.Bind(c)))
		} else {
			s.Value = f.value(c)
		}
		form.Fields = append(form.Fields, s)
	}
	return form
}

func (f *Field[C]) options(c *C) ([]Option, error) {
	opts := append([]Option(nil), f.Options...)
	if f.OptionsFrom == nil {
		return opts, nil
	}
	more, err := f.OptionsFrom(c)
	for _, o := range more {
		if !hasOption(opts, o.Value) {
			opts = append(opts, o)
		}
	}
	return opts, err
}

func hasOption(opts []Option, v string) bool {
	for _, o := range opts {
		if o.Value == v {
			return true
		}
	}
	return false
}

// value is the effective value: the default when the stored value is zero.
func (f *Field[C]) value(c *C) any {
	v := get(f.Bind(c))
	if f.Default != nil && isZero(v) {
		return f.Default
	}
	return v
}

// Value returns the effective value of key in c (nil for an unknown key).
func (r *Registry[C]) Value(c *C, key string) any {
	f, ok := r.Field(key)
	if !ok {
		return nil
	}
	return f.value(c)
}

// IsDefault reports whether key holds no value or one equal to its default
// (a file written by hand or by an older version may spell the default out).
func (r *Registry[C]) IsDefault(c *C, key string) bool {
	f, ok := r.Field(key)
	if !ok {
		return false
	}
	v := get(f.Bind(c))
	return isZero(v) || (f.Default != nil && equal(v, f.Default))
}

// Visible reports whether key is shown for c (its VisibleIf holds).
func (r *Registry[C]) Visible(c *C, key string) bool {
	f, ok := r.Field(key)
	if !ok {
		return false
	}
	if f.VisibleIf == nil {
		return true
	}
	return fmt.Sprint(r.Value(c, f.VisibleIf.Key)) == fmt.Sprint(f.VisibleIf.Equals)
}

// Display formats the effective value of key for a terminal row.
func (r *Registry[C]) Display(c *C, key string) string {
	f, ok := r.Field(key)
	if !ok {
		return ""
	}
	v := f.value(c)
	switch f.Type {
	case Secret:
		if isZero(v) {
			return "(not set)"
		}
		return "••••••"
	case Bool:
		if v == true {
			return "on"
		}
		return "off"
	case Number:
		if n, ok := toFloat(v); ok {
			return strconv.FormatFloat(n, 'f', decimals(f.Step), 64)
		}
	}
	s := fmt.Sprint(v)
	if opts, _ := f.options(c); f.Type == Select {
		for _, o := range opts {
			if o.Value == s {
				return o.Label
			}
		}
	}
	if s == "" {
		if f.Placeholder != nil {
			return "auto: " + f.Placeholder(c)
		}
		return "(not set)"
	}
	return s
}

// Set validates raw and stores it under key. raw may be a string (from a
// command line or text input) for every type. Storing the default stores the
// zero value.
func (r *Registry[C]) Set(c *C, key string, raw any) error {
	f, ok := r.Field(key)
	if !ok {
		return fmt.Errorf("unknown setting")
	}
	v, err := f.normalise(raw)
	if err != nil {
		return err
	}
	if err := f.validate(c, v); err != nil {
		return err
	}
	if f.Check != nil {
		if v, err = f.Check(c, v); err != nil {
			return err
		}
	}
	if f.Default != nil && equal(v, f.Default) {
		return store(f.Bind(c), nil)
	}
	return store(f.Bind(c), v)
}

// Reset stores the zero value of key, which means its default. It goes
// through Set, so a required value (a Check that refuses "") stays.
func (r *Registry[C]) Reset(c *C, key string) error {
	f, ok := r.Field(key)
	if !ok {
		return fmt.Errorf("unknown setting")
	}
	switch f.Type {
	case Number:
		return r.Set(c, key, 0.0)
	case Bool:
		return r.Set(c, key, false)
	}
	return r.Set(c, key, "")
}

// Step moves key one step in dir (-1 or +1): toggles a Bool, cycles a Select
// through its options, and moves a Number by Step within Min..Max, or
// through its suggested Options when it has some.
func (r *Registry[C]) Step(c *C, key string, dir int) error {
	f, ok := r.Field(key)
	if !ok {
		return fmt.Errorf("unknown setting")
	}
	cur := f.value(c)
	switch f.Type {
	case Bool:
		return r.Set(c, key, cur != true)
	case Select:
		opts, _ := f.options(c)
		if len(opts) == 0 {
			return fmt.Errorf("%s has no options to choose from", f.Label)
		}
		return r.Set(c, key, opts[cycle(indexOf(opts, fmt.Sprint(cur)), dir, len(opts))].Value)
	case Number:
		n, _ := toFloat(cur)
		if len(f.Options) > 0 {
			i := -1
			for j, o := range f.Options {
				if v, err := strconv.ParseFloat(o.Value, 64); err == nil && v == n {
					i = j
				}
			}
			return r.Set(c, key, f.Options[cycle(i, dir, len(f.Options))].Value)
		}
		if f.Step == nil {
			return fmt.Errorf("%s has no step", f.Label)
		}
		n = math.Round((n+float64(dir)**f.Step)/(*f.Step)) * *f.Step
		n = math.Round(n*1e9) / 1e9
		if f.Min != nil {
			n = math.Max(n, *f.Min)
		}
		if f.Max != nil {
			n = math.Min(n, *f.Max)
		}
		return r.Set(c, key, n)
	}
	return fmt.Errorf("%s is edited as text", f.Label)
}

// decimals is how many decimals a step needs (0.05 -> 2); -1 = as needed.
func decimals(step *float64) int {
	if step == nil || *step <= 0 || *step >= 1 {
		return -1
	}
	return int(math.Ceil(-math.Log10(*step) - 1e-9))
}

// cycle moves from index i (-1: not found) by dir, wrapping around.
func cycle(i, dir, n int) int {
	if i < 0 {
		if dir < 0 {
			return n - 1
		}
		return 0
	}
	return ((i+dir)%n + n) % n
}

func indexOf(opts []Option, v string) int {
	for i, o := range opts {
		if o.Value == v {
			return i
		}
	}
	return -1
}

// Update applies several values at once. Nothing changes unless every value
// and the whole-config Validate pass; errs maps keys to messages.
func (r *Registry[C]) Update(c *C, values map[string]any) (changed []string, errs map[string]string) {
	cp := *c
	errs = map[string]string{}
	for key, raw := range values {
		if err := r.Set(&cp, key, raw); err != nil {
			errs[key] = err.Error()
			continue
		}
		changed = append(changed, key)
	}
	if len(errs) == 0 && r.Validate != nil {
		if err := r.Validate(&cp); err != nil {
			errs[""] = err.Error()
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	sort.Strings(changed)
	*c = cp
	return changed, nil
}

// NeedsRestart reports whether any of keys takes a restart to apply.
func (r *Registry[C]) NeedsRestart(keys []string) bool {
	for _, k := range keys {
		if f, ok := r.Field(k); ok && r.apply(f) == Restart {
			return true
		}
	}
	return false
}

// Uncovered lists the JSON keys of C that are neither a field nor Internal,
// so a test can fail when a config value is added without a way to edit it.
func (r *Registry[C]) Uncovered() []string {
	known := map[string]bool{}
	for _, f := range r.Fields {
		known[f.Key] = true
	}
	for _, k := range r.Internal {
		known[k] = true
	}
	var missing []string
	t := reflect.TypeOf((*C)(nil)).Elem()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = sf.Name
		}
		if !known[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

// Revision is a short hash of the config, to detect concurrent changes.
func Revision[C any](c *C) string {
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// normalise turns raw into the canonical value of the field's type: string,
// float64 or bool.
func (f *Field[C]) normalise(raw any) (any, error) {
	switch f.Type {
	case Number:
		var n float64
		switch v := raw.(type) {
		case float64:
			n = v
		case int:
			n = float64(v)
		case json.Number:
			x, err := v.Float64()
			if err != nil {
				return nil, fmt.Errorf("must be a number")
			}
			n = x
		case string:
			x, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return nil, fmt.Errorf("must be a number")
			}
			n = x
		default:
			return nil, fmt.Errorf("must be a number")
		}
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, fmt.Errorf("must be a number")
		}
		return n, nil
	case Bool:
		switch v := raw.(type) {
		case bool:
			return v, nil
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "1", "true", "on", "yes":
				return true, nil
			case "0", "false", "off", "no", "":
				return false, nil
			}
		}
		return nil, fmt.Errorf("must be true or false")
	}
	s, ok := raw.(string)
	if !ok {
		return nil, fmt.Errorf("must be a string")
	}
	return strings.TrimSpace(s), nil
}

// validate applies the checks every field of a type gets.
func (f *Field[C]) validate(c *C, v any) error {
	switch f.Type {
	case Select:
		s := v.(string)
		if (s == "" && f.Default != nil) || f.AllowCustom {
			return nil
		}
		opts, err := f.options(c)
		if len(opts) == 0 && err != nil {
			return nil // the options could not be listed: trust the user
		}
		if !hasOption(opts, s) {
			vals := make([]string, len(opts))
			for i, o := range opts {
				vals[i] = o.Value
			}
			return fmt.Errorf("must be one of: %s", strings.Join(vals, ", "))
		}
	case Number:
		n := v.(float64)
		if n == 0 && f.Default != nil {
			return nil // zero resets to the default
		}
		switch {
		case f.Min != nil && f.Max != nil && (n < *f.Min || n > *f.Max):
			return fmt.Errorf("must be between %g and %g", *f.Min, *f.Max)
		case f.Min != nil && n < *f.Min:
			return fmt.Errorf("must be at least %g", *f.Min)
		case f.Max != nil && n > *f.Max:
			return fmt.Errorf("must be at most %g", *f.Max)
		}
	}
	return nil
}

func get(p any) any {
	switch p := p.(type) {
	case *string:
		return *p
	case *bool:
		return *p
	case *int:
		return *p
	case *float64:
		return *p
	}
	panic(fmt.Sprintf("settings: Bind must return *string, *bool, *int or *float64, not %T", p))
}

// store writes v (nil: the zero value) through the pointer p.
func store(p, v any) error {
	switch p := p.(type) {
	case *string:
		*p, _ = v.(string)
	case *bool:
		*p, _ = v.(bool)
	case *float64:
		*p, _ = v.(float64)
	case *int:
		if v == nil {
			*p = 0
			return nil
		}
		n, _ := toFloat(v)
		if n != math.Trunc(n) {
			return fmt.Errorf("must be a whole number")
		}
		*p = int(n)
	default:
		get(p) // panics with the explanation
	}
	return nil
}

func isZero(v any) bool {
	switch v := v.(type) {
	case float64:
		return v == 0
	case int:
		return v == 0
	}
	return v == "" || v == false || v == nil
}

func toFloat(v any) (float64, bool) {
	switch v := v.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case string:
		f, err := strconv.ParseFloat(v, 64)
		return f, err == nil
	}
	return 0, false
}

func equal(a, b any) bool {
	if x, ok := a.(float64); ok {
		if y, ok := toFloat(b); ok {
			return math.Abs(x-y) < 1e-9
		}
	}
	return fmt.Sprint(a) == fmt.Sprint(b)
}
