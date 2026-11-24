package dbclient

import (
	"strings"
	"testing"
)

// The Rule Builder's tree: nested and/or groups, values always bound, unknown columns refused.
func TestRowsWhereTree(t *testing.T) {
	m, id, _ := editFixture(t)
	names := func(w *Node) ([]string, error) {
		r, err := m.Rows(id, RowsRequest{Schema: "main", Table: "users", Where: w, OrderBy: "id"})
		var out []string
		for _, row := range r.Rows {
			out = append(out, *row[1])
		}
		return out, err
	}
	rule := func(col, op, v string, vs ...string) Node {
		return Node{Kind: "rule", Column: col, Op: op, Value: v, Values: vs}
	}
	cases := []struct {
		w    Node
		want string
	}{
		{Node{Kind: "group", Children: []Node{rule("name", "starts", "A")}}, "Ann"},
		{Node{Kind: "group", Combinator: "or", Children: []Node{rule("name", "=", "Ann"), rule("email", "ends", "b@x")}}, "Ann,Bob"},
		{Node{Kind: "group", Children: []Node{rule("id", "in", "", "2", "3"), {Kind: "group", Combinator: "or", Children: []Node{rule("name", "contains", "o")}}}}, "Bob"},
		{Node{Kind: "group", Not: true, Children: []Node{rule("name", "=", "Ann")}}, "Bob"},
		{Node{Kind: "group", Children: []Node{rule("id", "between", "", "1", "1")}}, "Ann"},
		{Node{Kind: "group", Children: []Node{rule("name", "contains", "")}}, "Ann,Bob"}, // no value yet: skipped
		{Node{Kind: "group", Children: []Node{rule("name", "=", "x' OR '1'='1")}}, ""},   // a value is only ever a value
	}
	for _, c := range cases {
		got, err := names(&c.w)
		if err != nil || strings.Join(got, ",") != c.want {
			t.Errorf("%+v: got %v %v, want %q", c.w, got, err, c.want)
		}
	}
	if _, err := names(&Node{Kind: "group", Children: []Node{rule("name; DROP TABLE users", "=", "x")}}); err == nil || !strings.Contains(err.Error(), "unknown column") {
		t.Errorf("unknown column accepted: %v", err)
	}
	deep := rule("name", "=", "Ann")
	for i := 0; i < 6; i++ {
		deep = Node{Kind: "group", Children: []Node{deep}}
	}
	if _, err := names(&deep); err == nil {
		t.Error("too deep a tree accepted")
	}
}
