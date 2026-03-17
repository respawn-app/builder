package format

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strings"

	"builder/internal/shared/textutil"
)

func Parse(src string) (Document, error) {
	s := scanner{lines: splitRawLines(src)}
	if !s.consumeExact("*** Begin Patch") {
		return Document{}, errors.New("patch must start with *** Begin Patch")
	}

	doc := Document{}
	for !s.done() {
		line := s.peek()
		switch {
		case line == "*** End Patch":
			s.next()
			if !s.done() {
				return Document{}, errors.New("unexpected content after *** End Patch")
			}
			return doc, nil
		case strings.HasPrefix(line, "*** Add File: "):
			head := strings.TrimPrefix(s.next(), "*** Add File: ")
			content := []string{}
			for !s.done() {
				n := s.peek()
				if strings.HasPrefix(n, "*** ") {
					break
				}
				if !strings.HasPrefix(n, "+") {
					return Document{}, fmt.Errorf("add file line must start with +: %q", n)
				}
				content = append(content, strings.TrimPrefix(s.next(), "+"))
			}
			doc.Hunks = append(doc.Hunks, AddFile{Path: head, Content: content})
		case strings.HasPrefix(line, "*** Delete File: "):
			path := strings.TrimPrefix(s.next(), "*** Delete File: ")
			doc.Hunks = append(doc.Hunks, DeleteFile{Path: path})
		case strings.HasPrefix(line, "*** Update File: "):
			path := strings.TrimPrefix(s.next(), "*** Update File: ")
			up := UpdateFile{Path: path}
			if !s.done() && strings.HasPrefix(s.peek(), "*** Move to: ") {
				up.MoveTo = strings.TrimPrefix(s.next(), "*** Move to: ")
			}
			for !s.done() {
				n := s.peek()
				if strings.HasPrefix(n, "*** ") {
					break
				}
				if n == "" {
					up.Changes = append(up.Changes, ChangeLine{Kind: ' ', Content: ""})
					s.next()
					continue
				}
				p := n[0]
				if p != ' ' && p != '+' && p != '-' && p != '@' {
					return Document{}, fmt.Errorf("invalid update line prefix in %q", n)
				}
				up.Changes = append(up.Changes, ChangeLine{Kind: rune(p), Content: n[1:]})
				s.next()
			}
			doc.Hunks = append(doc.Hunks, up)
		default:
			return Document{}, fmt.Errorf("unknown patch block: %q", line)
		}
	}

	return Document{}, errors.New("missing *** End Patch")
}

type scanner struct {
	lines []string
	idx   int
}

func (s *scanner) done() bool {
	return s.idx >= len(s.lines)
}

func (s *scanner) peek() string {
	if s.done() {
		return ""
	}
	return s.lines[s.idx]
}

func (s *scanner) next() string {
	v := s.peek()
	s.idx++
	return v
}

func (s *scanner) consumeExact(v string) bool {
	if s.peek() == v {
		s.next()
		return true
	}
	return false
}

func splitRawLines(in string) []string {
	in = textutil.NormalizeCRLF(in)
	reader := bufio.NewScanner(bytes.NewBufferString(in))
	out := []string{}
	for reader.Scan() {
		out = append(out, reader.Text())
	}
	return out
}
