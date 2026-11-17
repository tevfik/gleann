package indexing

import "strings"

// maxMemberCandidates bounds how many same-named members a call may fan out
// to. A name shared by more members of one package than this is too ambiguous
// to link (the call stays external instead of producing noise edges).
const maxMemberCandidates = 8

// memberDecl is one "Type::member" declaration (the naming of the tree-sitter
// languages: Python, JS/TS, Rust, Java, C#, Ruby, PHP, Swift, C++).
type memberDecl struct {
	FQN   string
	Class string // the Name up to the last "::"
}

// memberIndex maps "<package>.<member>" to the members of that name declared
// in the package. The tree-sitter call extractor names a callee by its bare
// identifier in the caller's package ("pkg.add"), which never equals a
// method's FQN ("pkg.Registry::add"); the index lets the write path bind such
// a call to the method(s) it can denote, the same union Kùzu produced at query
// time by base name.
type memberIndex map[string][]memberDecl

func joinFQN(pkg, name string) string {
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}

// memberKey splits a member declaration into its lookup key ("<package>.<member>")
// and class; ok is false for a name without "::".
func memberKey(fqn, name string) (key, class string, ok bool) {
	i := strings.LastIndex(name, "::")
	if i < 0 {
		return "", "", false
	}
	pkg := ""
	if fqn != name {
		pkg = strings.TrimSuffix(fqn, "."+name)
	}
	return joinFQN(pkg, name[i+2:]), name[:i], true
}

// add records a declaration when its name is a member ("Type::member").
func (m memberIndex) add(fqn, name string) {
	key, class, ok := memberKey(fqn, name)
	if !ok {
		return
	}
	for _, d := range m[key] {
		if d.FQN == fqn {
			return
		}
	}
	m[key] = append(m[key], memberDecl{FQN: fqn, Class: class})
}

// resolve returns the members a call from the symbol named srcName to the
// undeclared callee dst can denote, with the edge confidence. A member of the
// caller's own class wins; otherwise every member of that name is a candidate
// ("ambiguous" when more than one). Nothing is returned when there is no
// member or too many (maxMemberCandidates).
func (m memberIndex) resolve(srcName, dst string) ([]string, string) {
	cands := m[dst]
	if len(cands) == 0 {
		return nil, ""
	}
	if i := strings.LastIndex(srcName, "::"); i >= 0 {
		var same []memberDecl
		for _, c := range cands {
			if c.Class == srcName[:i] {
				same = append(same, c)
			}
		}
		if len(same) > 0 {
			cands = same
		}
	}
	if len(cands) > maxMemberCandidates {
		return nil, ""
	}
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.FQN
	}
	if len(out) == 1 {
		return out, "inferred"
	}
	return out, "ambiguous"
}
