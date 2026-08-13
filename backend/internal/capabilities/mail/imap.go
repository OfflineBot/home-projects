package mail

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// A small IMAP4rev1 client — only what fetching a mailbox needs: sign in,
// select a folder, fetch the newest messages whole, sign out.
//
// It is written out here rather than pulled in as a library for one reason:
// the single-use credential rule needs an unambiguous answer to "did the
// sign-in succeed?", and that is exactly the tagged OK to LOGIN below.

type imapClient struct {
	conn    net.Conn
	r       *bufio.Reader
	counter int
}

func dialIMAP(host string, port int, useTLS bool, timeout time.Duration) (*imapClient, error) {
	address := net.JoinHostPort(host, strconv.Itoa(port))
	var conn net.Conn
	var err error
	if useTLS {
		conn, err = tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", address,
			&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = net.DialTimeout("tcp", address, timeout)
	}
	if err != nil {
		return nil, fmt.Errorf("the mail server is not reachable: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))

	c := &imapClient{conn: conn, r: bufio.NewReaderSize(conn, 64*1024)}
	greeting, err := c.r.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("the mail server did not greet us: %w", err)
	}
	if !strings.HasPrefix(greeting, "* OK") {
		conn.Close()
		return nil, fmt.Errorf("the mail server answered %q", strings.TrimSpace(greeting))
	}
	return c, nil
}

func (c *imapClient) Close() { _ = c.conn.Close() }

func (c *imapClient) tag() string {
	c.counter++
	return fmt.Sprintf("a%03d", c.counter)
}

// command sends one command and collects the untagged lines until the tagged
// answer arrives.
func (c *imapClient) command(format string, args ...any) ([]string, error) {
	tag := c.tag()
	line := tag + " " + fmt.Sprintf(format, args...) + "\r\n"
	if _, err := c.conn.Write([]byte(line)); err != nil {
		return nil, err
	}
	var lines []string
	for {
		l, err := c.r.ReadString('\n')
		if err != nil {
			return lines, fmt.Errorf("the connection broke off: %w", err)
		}
		trimmed := strings.TrimRight(l, "\r\n")

		// A literal ({123}) means the next bytes are payload, not protocol.
		if idx := strings.LastIndex(trimmed, "{"); idx >= 0 && strings.HasSuffix(trimmed, "}") {
			size, err := strconv.Atoi(trimmed[idx+1 : len(trimmed)-1])
			if err == nil && size >= 0 {
				buf := make([]byte, size)
				if _, err := readFull(c.r, buf); err != nil {
					return lines, err
				}
				lines = append(lines, trimmed, string(buf))
				continue
			}
		}
		if strings.HasPrefix(trimmed, tag+" ") {
			rest := trimmed[len(tag)+1:]
			switch {
			case strings.HasPrefix(rest, "OK"):
				return lines, nil
			case strings.HasPrefix(rest, "NO"), strings.HasPrefix(rest, "BAD"):
				return lines, fmt.Errorf("%s", strings.TrimSpace(rest))
			}
			return lines, fmt.Errorf("unexpected answer: %s", rest)
		}
		lines = append(lines, trimmed)
	}
}

func readFull(r *bufio.Reader, buf []byte) (int, error) {
	read := 0
	for read < len(buf) {
		n, err := r.Read(buf[read:])
		read += n
		if err != nil {
			return read, err
		}
	}
	return read, nil
}

// Login is the one call whose success or failure decides whether the stored
// password survives.
func (c *imapClient) Login(user, password string) error {
	_, err := c.command("LOGIN %s %s", quote(user), quote(password))
	if err != nil {
		return fmt.Errorf("sign-in failed: %w", err)
	}
	return nil
}

func (c *imapClient) Select(mailbox string) (exists int, err error) {
	lines, err := c.command("SELECT %s", quote(mailbox))
	if err != nil {
		return 0, fmt.Errorf("the folder %q could not be opened: %w", mailbox, err)
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "* ") && strings.HasSuffix(l, " EXISTS") {
			fields := strings.Fields(l)
			if len(fields) >= 3 {
				exists, _ = strconv.Atoi(fields[1])
			}
		}
	}
	return exists, nil
}

// Message is one fetched mail, raw.
type Message struct {
	Seq  int
	UID  string
	Raw  []byte
	Size int
}

// FetchLatest returns the newest count messages of the selected mailbox.
func (c *imapClient) FetchLatest(exists, count int) ([]Message, error) {
	if exists == 0 || count <= 0 {
		return nil, nil
	}
	from := exists - count + 1
	if from < 1 {
		from = 1
	}
	lines, err := c.command("FETCH %d:%d (UID BODY.PEEK[])", from, exists)
	if err != nil {
		return nil, fmt.Errorf("the messages could not be fetched: %w", err)
	}

	var out []Message
	for i := 0; i < len(lines); i++ {
		header := lines[i]
		if !strings.HasPrefix(header, "* ") || !strings.Contains(header, "FETCH") {
			continue
		}
		if i+1 >= len(lines) {
			break
		}
		body := lines[i+1]
		i++
		msg := Message{Raw: []byte(body), Size: len(body)}
		fields := strings.Fields(header)
		if len(fields) > 1 {
			msg.Seq, _ = strconv.Atoi(fields[1])
		}
		if idx := strings.Index(header, "UID "); idx >= 0 {
			rest := header[idx+4:]
			end := strings.IndexAny(rest, " )")
			if end < 0 {
				end = len(rest)
			}
			msg.UID = strings.TrimSpace(rest[:end])
		}
		out = append(out, msg)
	}
	return out, nil
}

func (c *imapClient) Logout() { _, _ = c.command("LOGOUT") }

func quote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
