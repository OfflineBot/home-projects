package automation

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/store"
	"golang.org/x/crypto/ssh"
)

// The action registry. Adding one here is the same kind of change as adding a
// capability: no core file is touched.
func (Capability) Actions() []capability.Action {
	return []capability.Action{
		{
			Name:        "http",
			Title:       "HTTP request",
			Description: "Calls a URL. Several addresses at once, one per line.",
			Params:      []string{"method", "url", "headers", "body", "timeout", "expect"},
			Run:         runHTTP,
		},
		{
			Name:        "wol",
			Title:       "Wake-on-LAN",
			Description: "Sends the magic packet that wakes a machine.",
			Params:      []string{"mac", "broadcast", "port"},
			Run:         runWOL,
		},
		{
			Name:        "ssh",
			Title:       "SSH command",
			Description: "Runs a command on another machine — shutting the PC down, for instance.",
			Params:      []string{"host", "port", "user", "account", "command"},
			Run:         runSSH,
		},
		{
			Name:        "wled",
			Title:       "WLED",
			Description: "The lights: on, off, brightness, colour, an effect. Several at once.",
			Params:      []string{"host", "power", "brightness", "color", "effect", "preset"},
			Run:         runWLED,
		},
		{
			Name:        "ping",
			Title:       "Ping / port check",
			Description: "Checks whether something answers, and reports it as a variable.",
			Params:      []string{"host", "port", "variable"},
			Run:         runPing,
		},
		{
			Name:        "writefile",
			Title:       "Write file",
			Description: "Writes into a project — the previous action's answer, for example.",
			Params:      []string{"project", "path", "content"},
			Run:         runWriteFile,
		},
		{
			Name:        "scheduler",
			Title:       "Trigger scheduler",
			Description: "Runs a scheduler right now.",
			Params:      []string{"scheduler"},
			Run:         runScheduler,
		},
	}
}

// AccountKinds: the credentials the SSH action needs.
func (Capability) AccountKinds() []capability.AccountKind {
	return []capability.AccountKind{{
		Name:        "ssh",
		Title:       "SSH",
		Description: "A private key (or a password) for running commands on another machine.",
		Fields: []capability.AccountField{
			{Name: "user", Label: "User", Type: "text", Required: true},
			{Name: "host", Label: "Host", Type: "text", Placeholder: "192.168.178.50"},
			{Name: "passphrase", Label: "Key passphrase", Type: "password"},
		},
		SecretLabel: "Private key or password",
		SecretIsKey: true,
		Test: func(ctx context.Context, env *capability.Env, a *model.Account, secret []byte) error {
			cfg := accountConfig(a)
			host := cfg["host"]
			if host == "" {
				return fmt.Errorf("this account has no host to test against")
			}
			client, err := dialSSH(ctx, host, "22", cfg["user"], secret, cfg["passphrase"])
			if err != nil {
				return err
			}
			return client.Close()
		},
	}}
}

func param(in capability.ActionInput, name string) string {
	v, ok := in.Params[name]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return expand(t, in)
	case int:
		return strconv.Itoa(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return fmt.Sprint(v)
}

// expand fills in the one placeholder the chain needs: the previous action's
// answer.
func expand(s string, in capability.ActionInput) string {
	if in.Previous == nil {
		return strings.ReplaceAll(s, "{{previous}}", "")
	}
	return strings.ReplaceAll(s, "{{previous}}", in.Previous.Output)
}

func runHTTP(ctx context.Context, env *capability.Env, in capability.ActionInput) (capability.ActionResult, error) {
	// One address, or several: three lamps in a room are three addresses and
	// one intention, and writing the same rule three times is how one of them
	// ends up out of step.
	addresses := []string{}
	for _, raw := range strings.FieldsFunc(param(in, "url"), func(r rune) bool {
		return r == '\n' || r == ',' || r == ' '
	}) {
		if address := strings.TrimSpace(raw); address != "" {
			addresses = append(addresses, address)
		}
	}
	if len(addresses) == 0 {
		return capability.ActionResult{}, fmt.Errorf("this action needs a url")
	}
	if len(addresses) > 1 {
		return runHTTPMany(ctx, in, addresses)
	}
	url := addresses[0]
	method := strings.ToUpper(param(in, "method"))
	if method == "" {
		method = http.MethodGet
	}
	timeout := 20 * time.Second
	if t := param(in, "timeout"); t != "" {
		if d, err := time.ParseDuration(t); err == nil {
			timeout = d
		}
	}
	var body io.Reader
	if b := param(in, "body"); b != "" {
		body = strings.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return capability.ActionResult{}, err
	}
	if headers, ok := in.Params["headers"].(map[string]any); ok {
		for k, v := range headers {
			req.Header.Set(k, fmt.Sprint(v))
		}
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return capability.ActionResult{}, fmt.Errorf("not reachable: %w", err)
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	expect := param(in, "expect")
	if expect != "" {
		want, err := strconv.Atoi(expect)
		if err == nil && resp.StatusCode != want {
			return capability.ActionResult{}, fmt.Errorf("answered %s, expected %d", resp.Status, want)
		}
	} else if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return capability.ActionResult{}, fmt.Errorf("answered %s", resp.Status)
	}
	in.Log("%s %s → %s", method, url, resp.Status)
	return capability.ActionResult{Output: string(answer), Value: resp.StatusCode}, nil
}

// runWOL builds the magic packet by hand: six 0xFF bytes and the MAC sixteen
// times over.
func runWOL(ctx context.Context, env *capability.Env, in capability.ActionInput) (capability.ActionResult, error) {
	macStr := param(in, "mac")
	mac, err := parseMAC(macStr)
	if err != nil {
		return capability.ActionResult{}, err
	}
	broadcast := param(in, "broadcast")
	if broadcast == "" {
		broadcast = "255.255.255.255"
	}
	port := param(in, "port")
	if port == "" {
		port = "9"
	}

	packet := bytes.Repeat([]byte{0xFF}, 6)
	for i := 0; i < 16; i++ {
		packet = append(packet, mac...)
	}
	conn, err := net.Dial("udp", net.JoinHostPort(broadcast, port))
	if err != nil {
		return capability.ActionResult{}, fmt.Errorf("the packet could not be sent: %w", err)
	}
	defer conn.Close()
	if _, err := conn.Write(packet); err != nil {
		return capability.ActionResult{}, fmt.Errorf("the packet could not be sent: %w", err)
	}
	in.Log("magic packet sent to %s via %s:%s", macStr, broadcast, port)
	return capability.ActionResult{Output: "magic packet sent to " + macStr}, nil
}

func parseMAC(s string) ([]byte, error) {
	clean := strings.NewReplacer(":", "", "-", "", ".", "", " ", "").Replace(s)
	if len(clean) != 12 {
		return nil, fmt.Errorf("%q is not a MAC address", s)
	}
	return hex.DecodeString(clean)
}

func runSSH(ctx context.Context, env *capability.Env, in capability.ActionInput) (capability.ActionResult, error) {
	host := param(in, "host")
	command := param(in, "command")
	if host == "" || command == "" {
		return capability.ActionResult{}, fmt.Errorf("this action needs a host and a command")
	}
	port := param(in, "port")
	if port == "" {
		port = "22"
	}
	user := param(in, "user")
	accountRef := param(in, "account")
	if accountRef == "" {
		return capability.ActionResult{}, fmt.Errorf("this action needs an ssh account — credentials live in the accounts menu, not in the project")
	}
	accountID, err := uuid.Parse(accountRef)
	if err != nil {
		return capability.ActionResult{}, fmt.Errorf("%q is not an account id", accountRef)
	}
	account, err := env.Store.AccountByID(ctx, accountID)
	if err != nil {
		return capability.ActionResult{}, fmt.Errorf("there is no such account")
	}
	cfg := accountConfig(account)
	if user == "" {
		user = cfg["user"]
	}

	var output string
	err = env.UseAccount(ctx, accountID, func(secretBytes []byte) error {
		client, err := dialSSH(ctx, host, port, user, secretBytes, cfg["passphrase"])
		if err != nil {
			return err
		}
		defer client.Close()
		session, err := client.NewSession()
		if err != nil {
			return err
		}
		defer session.Close()
		out, err := session.CombinedOutput(command)
		output = string(out)
		if err != nil {
			return fmt.Errorf("%v: %s", err, trim(output, 200))
		}
		return nil
	})
	if err != nil {
		return capability.ActionResult{}, err
	}
	in.Log("ssh %s@%s: %s", user, host, command)
	return capability.ActionResult{Output: output}, nil
}

func dialSSH(ctx context.Context, host, port, user string, secretBytes []byte, passphrase string) (*ssh.Client, error) {
	if user == "" {
		return nil, fmt.Errorf("no user for the ssh connection")
	}
	var auths []ssh.AuthMethod
	trimmed := strings.TrimSpace(string(secretBytes))
	if strings.Contains(trimmed, "PRIVATE KEY") {
		var signer ssh.Signer
		var err error
		if passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(secretBytes, []byte(passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(secretBytes)
		}
		if err != nil {
			return nil, fmt.Errorf("the private key could not be read: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	} else if trimmed != "" {
		auths = append(auths, ssh.Password(trimmed))
	}
	if len(auths) == 0 {
		return nil, fmt.Errorf("the account has neither a key nor a password")
	}

	dialer := net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, fmt.Errorf("not reachable: %w", err)
	}
	// The host key is not pinned: this talks to machines on the home network,
	// and demanding a known_hosts file here would only be theatre.
	clientConn, chans, reqs, err := ssh.NewClientConn(conn, net.JoinHostPort(host, port), &ssh.ClientConfig{
		User:            user,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("sign-in failed: %w", err)
	}
	return ssh.NewClient(clientConn, chans, reqs), nil
}

func runPing(ctx context.Context, env *capability.Env, in capability.ActionInput) (capability.ActionResult, error) {
	host := param(in, "host")
	if host == "" {
		return capability.ActionResult{}, fmt.Errorf("this action needs a host")
	}
	ports := []string{param(in, "port")}
	if ports[0] == "" {
		ports = []string{"22", "80", "443", "445", "3389"}
	}
	online := false
	for _, p := range ports {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, p), 2*time.Second)
		if err == nil {
			_ = conn.Close()
			online = true
			break
		}
	}
	name := param(in, "variable")
	if name == "" {
		name = "online"
	}
	in.Log("%s is %s", host, map[bool]string{true: "up", false: "down"}[online])
	return capability.ActionResult{
		Output: fmt.Sprint(online),
		Value:  online,
		Variables: []store.VariableInput{
			{Name: name, Type: "bool", Value: online, History: true},
		},
	}, nil
}

func runWriteFile(ctx context.Context, env *capability.Env, in capability.ActionInput) (capability.ActionResult, error) {
	target := in.Project
	if ref := param(in, "project"); ref != "" {
		found, err := findProject(ctx, env, ref)
		if err != nil {
			return capability.ActionResult{}, err
		}
		target = found
	}
	path := param(in, "path")
	if path == "" {
		return capability.ActionResult{}, fmt.Errorf("this action needs a path")
	}
	content := param(in, "content")
	if content == "" && in.Previous != nil {
		content = in.Previous.Output
	}
	if _, err := env.Files.Write(ctx, target, path, []byte(content), files.Op{
		Author: "an automation", Email: "automation@home-projects",
		Message: "Automation wrote " + path, Commit: true,
	}); err != nil {
		return capability.ActionResult{}, err
	}
	in.Log("wrote %d bytes to %s in %s", len(content), path, target.Title)
	return capability.ActionResult{Output: path}, nil
}

func runScheduler(ctx context.Context, env *capability.Env, in capability.ActionInput) (capability.ActionResult, error) {
	ref := param(in, "scheduler")
	if ref == "" {
		return capability.ActionResult{}, fmt.Errorf("this action needs a scheduler id")
	}
	id, err := uuid.Parse(ref)
	if err != nil {
		return capability.ActionResult{}, fmt.Errorf("%q is not a scheduler id", ref)
	}
	if env.RunScheduler == nil {
		return capability.ActionResult{}, fmt.Errorf("this server cannot start schedulers from automations")
	}
	if err := env.RunScheduler(ctx, id, "automation"); err != nil {
		return capability.ActionResult{}, err
	}
	in.Log("scheduler %s ran", ref)
	return capability.ActionResult{Output: "scheduler ran"}, nil
}

func findProject(ctx context.Context, env *capability.Env, ref string) (*model.Project, error) {
	if id, err := uuid.Parse(ref); err == nil {
		return env.Store.ProjectByID(ctx, id)
	}
	matches, err := env.Store.ProjectsBySlug(ctx, ref)
	if err != nil {
		return nil, err
	}
	if len(matches) != 1 {
		return nil, fmt.Errorf("%q does not name exactly one project", ref)
	}
	return &matches[0], nil
}

// accountConfig unpacks the small JSON object an account keeps its
// non-secret fields in.
func accountConfig(a *model.Account) map[string]string {
	out := map[string]string{}
	var raw map[string]any
	if err := json.Unmarshal(a.Config, &raw); err != nil {
		return out
	}
	for k, v := range raw {
		out[k] = fmt.Sprint(v)
	}
	return out
}

// runHTTPMany sends the same request to every address and reports as one. All
// at once rather than one after another: the point of naming three lamps is
// that they change together.
func runHTTPMany(ctx context.Context, in capability.ActionInput, addresses []string) (capability.ActionResult, error) {
	type answer struct {
		address string
		status  int
		err     error
	}
	results := make(chan answer, len(addresses))
	for _, address := range addresses {
		go func(address string) {
			one := in
			one.Params = map[string]any{}
			for k, v := range in.Params {
				one.Params[k] = v
			}
			one.Params["url"] = address
			out, err := runHTTP(ctx, nil, one)
			status, _ := out.Value.(int)
			results <- answer{address: address, status: status, err: err}
		}(address)
	}
	var failed []string
	lines := []string{}
	for range addresses {
		r := <-results
		if r.err != nil {
			failed = append(failed, r.address+": "+r.err.Error())
			lines = append(lines, r.address+" → "+r.err.Error())
			continue
		}
		lines = append(lines, fmt.Sprintf("%s → %d", r.address, r.status))
	}
	sort.Strings(lines)
	in.Log("%d addresses: %s", len(addresses), strings.Join(lines, ", "))
	if len(failed) > 0 {
		return capability.ActionResult{Output: strings.Join(lines, "\n")},
			fmt.Errorf("%d of %d failed: %s", len(failed), len(addresses), strings.Join(failed, "; "))
	}
	return capability.ActionResult{Output: strings.Join(lines, "\n"), Value: len(addresses)}, nil
}

// runWLED speaks to the lights directly.
//
// WLED has an HTTP interface, so this could be an http action with a hand-built
// JSON body — and that is exactly what it was, which meant looking up the field
// names every time. On/off, brightness, a colour, one of the effects: that is
// what a person wants from a lamp, and the rest is still reachable the long way.
func runWLED(ctx context.Context, env *capability.Env, in capability.ActionInput) (capability.ActionResult, error) {
	hosts := []string{}
	for _, raw := range strings.FieldsFunc(param(in, "host"), func(r rune) bool {
		return r == '\n' || r == ',' || r == ' '
	}) {
		if host := strings.TrimSpace(raw); host != "" {
			hosts = append(hosts, host)
		}
	}
	if len(hosts) == 0 {
		return capability.ActionResult{}, fmt.Errorf("this action needs the address of a WLED")
	}

	state := map[string]any{}
	switch strings.ToLower(param(in, "power")) {
	case "on", "true", "1":
		state["on"] = true
	case "off", "false", "0":
		state["on"] = false
	case "toggle", "":
		if param(in, "power") == "toggle" {
			state["on"] = "t" // WLED's own word for "the other one"
		}
	}
	if b := param(in, "brightness"); b != "" {
		n, err := strconv.Atoi(b)
		if err != nil || n < 0 || n > 255 {
			return capability.ActionResult{}, fmt.Errorf("brightness is 0 to 255, not %q", b)
		}
		state["bri"] = n
	}
	segment := map[string]any{}
	if colour := strings.TrimSpace(param(in, "color")); colour != "" {
		rgb, err := parseColour(colour)
		if err != nil {
			return capability.ActionResult{}, err
		}
		segment["col"] = []any{rgb}
	}
	if effect := param(in, "effect"); effect != "" {
		if n, err := strconv.Atoi(effect); err == nil {
			segment["fx"] = n
		} else {
			segment["fx"] = effect
		}
	}
	if preset := param(in, "preset"); preset != "" {
		if n, err := strconv.Atoi(preset); err == nil {
			state["ps"] = n
		}
	}
	if len(segment) > 0 {
		state["seg"] = []any{segment}
	}
	if len(state) == 0 {
		return capability.ActionResult{}, fmt.Errorf("nothing to change — say on, off, a brightness or a colour")
	}
	body, err := json.Marshal(state)
	if err != nil {
		return capability.ActionResult{}, err
	}

	done := []string{}
	for _, host := range hosts {
		address := host
		if !strings.HasPrefix(address, "http") {
			address = "http://" + address
		}
		address = strings.TrimSuffix(address, "/") + "/json/state"
		req, rerr := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
		if rerr != nil {
			return capability.ActionResult{}, rerr
		}
		req.Header.Set("Content-Type", "application/json")
		resp, derr := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if derr != nil {
			return capability.ActionResult{Output: strings.Join(done, "\n")},
				fmt.Errorf("%s is not answering: %w", host, derr)
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return capability.ActionResult{Output: strings.Join(done, "\n")},
				fmt.Errorf("%s answered %s", host, resp.Status)
		}
		done = append(done, host+" → "+resp.Status)
	}
	in.Log("wled %s: %s", strings.Join(hosts, ", "), string(body))
	return capability.ActionResult{Output: strings.Join(done, "\n"), Value: len(hosts)}, nil
}

// parseColour reads "#ff8800", "ff8800" or "255,136,0".
func parseColour(in string) ([]int, error) {
	in = strings.TrimSpace(strings.TrimPrefix(in, "#"))
	if strings.Contains(in, ",") {
		parts := strings.Split(in, ",")
		if len(parts) != 3 {
			return nil, fmt.Errorf("%q is not a colour — three numbers or a hex code", in)
		}
		out := make([]int, 3)
		for i, p := range parts {
			n, err := strconv.Atoi(strings.TrimSpace(p))
			if err != nil || n < 0 || n > 255 {
				return nil, fmt.Errorf("%q is not a colour — each part is 0 to 255", in)
			}
			out[i] = n
		}
		return out, nil
	}
	if len(in) != 6 {
		return nil, fmt.Errorf("%q is not a colour — six hex digits, or three numbers", in)
	}
	value, err := strconv.ParseUint(in, 16, 32)
	if err != nil {
		return nil, fmt.Errorf("%q is not a colour", in)
	}
	return []int{int(value >> 16 & 0xff), int(value >> 8 & 0xff), int(value & 0xff)}, nil
}
