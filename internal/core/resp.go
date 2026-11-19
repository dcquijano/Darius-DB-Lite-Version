package core

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// A tiny RESP2 client - enough for the built-in Redis browser and shutdown,
// without pulling in a full Redis library.

type redisConn struct {
	c net.Conn
	r *bufio.Reader
}

type RedisError string

func (e RedisError) Error() string { return string(e) }

func dialRedis(s EngineSettings, db int) (*redisConn, error) {
	c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(s.Port), 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to Redis on port %d (is it running?)", s.Port)
	}
	rc := &redisConn{c: c, r: bufio.NewReader(c)}
	if s.Password != "" {
		if _, err := rc.Do("AUTH", s.Password); err != nil {
			rc.Close()
			return nil, err
		}
	}
	if db > 0 {
		if _, err := rc.Do("SELECT", strconv.Itoa(db)); err != nil {
			rc.Close()
			return nil, err
		}
	}
	return rc, nil
}

func (rc *redisConn) Close() error { return rc.c.Close() }

func (rc *redisConn) Do(args ...string) (interface{}, error) {
	_ = rc.c.SetDeadline(time.Now().Add(15 * time.Second))
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := io.WriteString(rc.c, b.String()); err != nil {
		return nil, err
	}
	v, err := rc.read()
	if err != nil {
		return nil, err
	}
	if e, ok := v.(RedisError); ok {
		return nil, e
	}
	return v, nil
}

func (rc *redisConn) read() (interface{}, error) {
	line, err := rc.r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if line == "" {
		return nil, errors.New("empty reply")
	}
	body := line[1:]
	switch line[0] {
	case '+':
		return body, nil
	case '-':
		return RedisError(body), nil
	case ':':
		n, _ := strconv.ParseInt(body, 10, 64)
		return n, nil
	case '$':
		n, _ := strconv.Atoi(body)
		if n < 0 {
			return nil, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(rc.r, buf); err != nil {
			return nil, err
		}
		return string(buf[:n]), nil
	case '*':
		n, _ := strconv.Atoi(body)
		if n < 0 {
			return nil, nil
		}
		out := make([]interface{}, n)
		for i := 0; i < n; i++ {
			v, err := rc.read()
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}
	return nil, fmt.Errorf("unexpected reply: %q", line)
}

// formatRedis renders a reply like redis-cli does.
func formatRedis(v interface{}, indent string) string {
	switch t := v.(type) {
	case nil:
		return "(nil)"
	case string:
		return strconv.Quote(t)
	case int64:
		return "(integer) " + strconv.FormatInt(t, 10)
	case RedisError:
		return "(error) " + string(t)
	case []interface{}:
		if len(t) == 0 {
			return "(empty array)"
		}
		var b strings.Builder
		w := len(strconv.Itoa(len(t)))
		for i, item := range t {
			prefix := fmt.Sprintf("%*d) ", w, i+1)
			if i > 0 {
				b.WriteString("\n" + indent)
			}
			b.WriteString(prefix + formatRedis(item, indent+strings.Repeat(" ", len(prefix))))
		}
		return b.String()
	}
	return fmt.Sprint(v)
}

// splitArgs splits a command line respecting "double" and 'single' quotes.
func splitArgs(line string) ([]string, error) {
	var args []string
	var cur strings.Builder
	inQ := rune(0)
	has := false
	esc := false
	for _, r := range line {
		switch {
		case esc:
			switch r {
			case 'n':
				cur.WriteRune('\n')
			case 't':
				cur.WriteRune('\t')
			default:
				cur.WriteRune(r)
			}
			esc = false
		case r == '\\' && inQ == '"':
			esc = true
		case inQ != 0:
			if r == inQ {
				inQ = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			inQ, has = r, true
		case r == ' ' || r == '\t':
			if has {
				args = append(args, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if inQ != 0 {
		return nil, errors.New("unbalanced quotes")
	}
	if has {
		args = append(args, cur.String())
	}
	return args, nil
}
