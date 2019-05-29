package qcode

import (
	"errors"
	"fmt"
	"sync"
)

var (
	errEOT = errors.New("end of tokens")
)

type parserType int

const (
	maxFields = 100

	parserError parserType = iota
	parserEOF
	opQuery
	opMutate
	opSub
	nodeStr
	nodeInt
	nodeFloat
	nodeBool
	nodeObj
	nodeList
	nodeVar
)

type Operation struct {
	Type    parserType
	Name    []byte
	Args    []Arg
	argsA   [10]Arg
	Fields  []Field
	fieldsA [10]Field
}

var (
	zeroField = Field{}
	zeroArg   = Arg{}
)

func (op *Operation) Reset() {
	op.Type = 0
	op.Name = op.Name[:]

	for i := range op.Args {
		op.Args[i] = zeroArg
	}
	for i := range op.argsA {
		op.argsA[i] = zeroArg
	}

	for i := range op.Fields {
		op.Fields[i] = zeroField
	}
	for i := range op.fieldsA {
		op.fieldsA[i] = zeroField
	}
}

type Field struct {
	ID        int
	ParentID  int
	Name      []byte
	Alias     []byte
	Args      []Arg
	argsA     [10]Arg
	Children  []int
	childrenA [10]int
}

type Arg struct {
	Name []byte
	Val  []Node
	valA [10]Node
}

type Node struct {
	ID        int
	ParentID  int
	Type      parserType
	Name      []byte
	Val       []byte
	Children  []int
	childrenA [10]int
}

type Parser struct {
	input []byte // the string being scanned
	pos   int
	items []item
	depth int
	err   error
}

var opPool = sync.Pool{
	New: func() interface{} {
		return new(Operation)
	},
}

func Parse(gql []byte) (*Operation, error) {
	if len(gql) == 0 {
		return nil, errors.New("blank query")
	}

	l, err := lex(gql)
	if err != nil {
		return nil, err
	}

	p := &Parser{
		input: l.input,
		pos:   -1,
		items: l.items,
	}
	return p.parseOp()
}

func ParseQuery(gql []byte) (*Operation, error) {
	return parseByType(gql, opQuery)
}

func ParseArgValue(argVal []byte) ([]Node, error) {
	l, err := lex(argVal)
	if err != nil {
		return nil, err
	}
	p := &Parser{
		input: l.input,
		pos:   -1,
		items: l.items,
	}

	return p.parseValue(make([]Node, 0, 10), -1)
}

func (p *Parser) parseValue(nodes []Node, pid int) ([]Node, error) {
	if p.peek(itemListOpen) {
		p.ignore()
		return p.parseList(nodes, pid)
	}

	if p.peek(itemObjOpen) {
		p.ignore()
		return p.parseObj(nodes, pid)
	}

	item := p.next()
	node := Node{
		ID:       len(nodes),
		ParentID: pid,
		Val:      p.val(item),
	}
	node.Children = node.childrenA[:0]

	if pid != -1 {
		nodes[pid].Children = append(nodes[pid].Children, node.ID)
	}

	switch item.typ {
	case itemIntVal:
		node.Type = nodeInt
	case itemFloatVal:
		node.Type = nodeFloat
	case itemStringVal:
		node.Type = nodeStr
	case itemBoolVal:
		node.Type = nodeBool
	case itemName:
		node.Type = nodeStr
	case itemVariable:
		node.Type = nodeVar
	default:
		return nil, fmt.Errorf("expecting a number, string, object, list or variable as an argument value (not %s)", p.val(p.next()))
	}

	return append(nodes, node), nil
}

func (p *Parser) parseList(nodes []Node, pid int) ([]Node, error) {
	var ty parserType
	var err error

	node := Node{
		ID:       len(nodes),
		ParentID: pid,
		Type:     nodeList,
	}
	node.Children = node.childrenA[:0]

	if pid != -1 {
		nodes[pid].Children = append(nodes[pid].Children, node.ID)
	}

	nodes = append(nodes, node)

	lc := 0
	for {
		if p.peek(itemListClose) {
			p.ignore()
			break
		}
		nodes, err = p.parseValue(nodes, node.ID)
		if err != nil {
			return nil, err
		}

		if ty == 0 {
			ty = node.Type

		} else if ty != node.Type {
			return nil, errors.New("All values in a list must be of the same type")

		}
		lc++
	}

	if lc == 0 {
		return nil, errors.New("List cannot be empty")
	}

	return nodes, nil
}

func (p *Parser) parseObj(nodes []Node, pid int) ([]Node, error) {
	var err error

	node := Node{
		ID:       len(nodes),
		ParentID: pid,
		Type:     nodeObj,
	}
	node.Children = node.childrenA[:0]

	if pid != -1 {
		nodes[pid].Children = append(nodes[pid].Children, node.ID)
	}

	nodes = append(nodes, node)

	for {

		if p.peek(itemObjClose) {
			p.ignore()
			break
		}

		if p.peek(itemName) == false {
			return nil, errors.New("expecting an argument name")
		}
		nodeName := p.val(p.next())

		if p.peek(itemColon) == false {
			return nil, errors.New("missing ':' after field argument name")
		}
		p.ignore()

		id := len(nodes)
		nodes, err = p.parseValue(nodes, node.ID)
		if err != nil {
			return nil, err
		}

		nodes[id].Name = nodeName
	}

	return nodes, nil
}

func parseByType(gql []byte, ty parserType) (*Operation, error) {
	l, err := lex(gql)
	if err != nil {
		return nil, err
	}

	p := &Parser{
		input: l.input,
		pos:   -1,
		items: l.items,
	}
	return p.parseOpByType(ty)
}

func (p *Parser) next() item {
	n := p.pos + 1
	if n >= len(p.items) {
		p.err = errEOT
		return item{typ: itemEOF}
	}
	p.pos = n
	return p.items[p.pos]
}

func (p *Parser) ignore() {
	n := p.pos + 1
	if n >= len(p.items) {
		p.err = errEOT
		return
	}
	p.pos = n
}

func (p *Parser) current() item {
	return p.items[p.pos]
}

func (p *Parser) eof() bool {
	n := p.pos + 1
	return p.items[n].typ == itemEOF
}

func (p *Parser) peek(types ...itemType) bool {
	n := p.pos + 1
	if p.items[n].typ == itemEOF {
		return false
	}
	if n >= len(p.items) {
		return false
	}
	for i := 0; i < len(types); i++ {
		if p.items[n].typ == types[i] {
			return true
		}
	}
	return false
}

func (p *Parser) parseOpByType(ty parserType) (*Operation, error) {
	var err error

	op := opPool.Get().(*Operation)
	op.Reset()
	op.Type = ty

	if ty == opQuery {
		if p.peek(itemQuery) {
			op.Name = p.val(p.next())
		}
	} else {
		return nil, errors.New("unsupported operation")
	}

	if p.peek(itemArgsOpen) {
		p.ignore()
		op.Args, err = p.parseArgs(op.argsA[:0])
		if err != nil {
			return nil, err
		}
	}

	if p.peek(itemObjOpen) {
		p.ignore()
		op.Fields, err = p.parseFields(op.fieldsA[:0])
		if err != nil {
			return nil, err
		}
	}

	if p.peek(itemObjClose) {
		p.ignore()
	}

	return op, nil
}

func (p *Parser) parseOp() (*Operation, error) {
	if p.peek(itemQuery, itemMutation, itemSub) == false {
		err := fmt.Errorf("expecting a query, mutation or subscription (not '%s')",
			p.val(p.next()))
		return nil, err
	}

	item := p.next()

	switch item.typ {
	case itemQuery:
		return p.parseOpByType(opQuery)
	case itemMutation:
		return p.parseOpByType(opMutate)
	case itemSub:
		return p.parseOpByType(opSub)
	}

	return nil, errors.New("unknown operation type")
}

func (p *Parser) parseFields(fields []Field) ([]Field, error) {
	st := NewStack()

	for {
		if len(fields) >= maxFields {
			return nil, fmt.Errorf("field limit reached (%d)", maxFields)
		}

		if p.peek(itemObjClose) {
			p.ignore()
			st.Pop()

			if st.Len() == 0 {
				break
			}
			continue
		}

		if p.peek(itemName) == false {
			return nil, errors.New("expecting an alias or field name")
		}

		fields = append(fields, Field{ID: len(fields)})
		f := &fields[(len(fields) - 1)]
		f.Children = f.childrenA[:0]

		if f.ID == 0 {
			f.ParentID = -1
		}

		if err := p.parseField(f); err != nil {
			return nil, err
		}

		if f.ID != 0 {
			pid := st.Peek()
			f.ParentID = pid
			fields[pid].Children = append(fields[pid].Children, f.ID)
		}

		if p.peek(itemObjOpen) {
			p.ignore()
			st.Push(f.ID)
		}
	}

	return fields, nil
}

func (p *Parser) parseField(f *Field) error {
	var err error
	f.Name = p.val(p.next())

	if p.peek(itemColon) {
		p.ignore()

		if p.peek(itemName) {
			f.Alias = f.Name
			f.Name = p.val(p.next())
		} else {
			return errors.New("expecting an aliased field name")
		}
	}

	if p.peek(itemArgsOpen) {
		p.ignore()
		if f.Args, err = p.parseArgs(f.argsA[:0]); err != nil {
			return err
		}
	}

	return nil
}

func (p *Parser) parseArgs(args []Arg) ([]Arg, error) {
	var err error

	for {
		if p.peek(itemArgsClose) {
			p.ignore()
			break
		}
		if p.peek(itemName) == false {
			return nil, errors.New("expecting an argument name")
		}
		if len(args) >= 20 {
			return nil, errors.New("too many arguments")
		}
		args = append(args, Arg{Name: p.val(p.next())})
		arg := &args[(len(args) - 1)]

		if p.peek(itemColon) == false {
			return nil, errors.New("missing ':' after argument name")
		}
		p.ignore()

		arg.Val, err = p.parseValue(arg.Val[:0], -1)
		if err != nil {
			return nil, err
		}
	}

	return args, nil
}

func (p *Parser) val(v item) []byte {
	return p.input[v.pos:v.end]
}

func (t parserType) String() string {
	var v string

	switch t {
	case parserEOF:
		v = "EOF"
	case parserError:
		v = "error"
	case opQuery:
		v = "query"
	case opMutate:
		v = "mutation"
	case opSub:
		v = "subscription"
	case nodeStr:
		v = "node-string"
	case nodeInt:
		v = "node-int"
	case nodeFloat:
		v = "node-float"
	case nodeBool:
		v = "node-bool"
	case nodeVar:
		v = "node-var"
	case nodeObj:
		v = "node-obj"
	case nodeList:
		v = "node-list"
	}
	return fmt.Sprintf("<%s>", v)
}
