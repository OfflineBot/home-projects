// Package machines is the other end of wake-on-LAN: the machines themselves.
//
// Waking a PC was already possible as a rule, and a rule is the right shape for
// "every morning at seven". It is the wrong shape for standing in front of the
// page wanting to know whether the thing is on, shut it down, or look at what
// is running in the tmux session that has been open for three weeks.
//
// So a machine is an entry in machines.json — a file in the project, like
// everything else, which means it is versioned, exported and readable without
// this capability. What is never in that file is a password.
package machines

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/offlinebot/home-projects/backend/internal/capability"
	"github.com/offlinebot/home-projects/backend/internal/files"
	"github.com/offlinebot/home-projects/backend/internal/model"
	"github.com/offlinebot/home-projects/backend/internal/slug"
	"golang.org/x/crypto/ssh"
)

// File is where the machines live.
const File = "machines.json"

type Machine struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port,omitempty"`
	User string `json:"user,omitempty"`
	MAC  string `json:"mac,omitempty"`
	// Broadcast is where the magic packet goes; empty means the network's own.
	Broadcast string `json:"broadcast,omitempty"`
	// Account names a stored SSH account to sign in with. Without one the
	// password is typed in, and is never stored anywhere.
	Account string `json:"account,omitempty"`
	Note    string `json:"note,omitempty"`
}

type List struct {
	Machines []Machine `json:"machines"`
}

type Capability struct{ capability.Base }

func New() Capability { return Capability{} }

func (Capability) Name() string   { return "machines" }
func (Capability) Title() string  { return "Machines" }
func (Capability) Icon() string   { return "server" }
func (Capability) Owns() []string { return []string{File} }

func (Capability) Presets() []capability.Preset {
	return []capability.Preset{{
		Key:         "machines",
		Title:       "Machines",
		Description: "Other computers: whether they are up, wake and shut down, and their tmux sessions.",
		Icon:        "server",
		DefaultTab:  "machines",
		// The rules come along, because "wake it every morning" belongs beside
		// "wake it now".
		Capabilities: []string{"machines", "automation"},
		Seed: []capability.SeedFile{{
			Path: File,
			Content: func(p *model.Project) []byte {
				return []byte("{\n  \"machines\": []\n}\n")
			},
		}},
	}}
}

// ------------------------------------------------------------------- reading

func read(ctx context.Context, env *capability.Env, p *model.Project) List {
	out := List{Machines: []Machine{}}
	body, err := env.Files.ReadLocal(ctx, p, File)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(body, &out)
	if out.Machines == nil {
		out.Machines = []Machine{}
	}
	return out
}

func write(ctx context.Context, env *capability.Env, p *model.Project, l List, author, email string) error {
	body, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	_, err = env.Files.Write(ctx, p, File, append(body, '\n'), files.Op{
		Author: author, Email: email, Message: "Change the machines", Commit: true,
	})
	return err
}

func find(l List, name string) (Machine, bool) {
	for _, m := range l.Machines {
		if strings.EqualFold(m.Name, name) || slug.Make(m.Name) == name {
			return m, true
		}
	}
	return Machine{}, false
}

func (m Machine) port() string {
	if m.Port > 0 {
		return strconv.Itoa(m.Port)
	}
	return "22"
}

// up answers the only question that matters before any other: is it on? A TCP
// connection to its SSH port, because that is what "on and reachable" means for
// everything else here.
func (m Machine) up(ctx context.Context) bool {
	dialer := net.Dialer{Timeout: 2500 * time.Millisecond}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(m.Host, m.port()))
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// ------------------------------------------------------------------ the wire

// signIn opens one SSH connection. The password, when there is one, came with
// the request and is gone when it returns — nothing writes it down.
func (c Capability) signIn(ctx context.Context, env *capability.Env, m Machine, password string) (*ssh.Client, error) {
	if m.User == "" && m.Account == "" {
		return nil, fmt.Errorf("this machine has no user to sign in as")
	}
	// A machine that names a stored key signs in with it, and nobody is asked
	// anything. A key is not spent by a failed attempt, so this can be retried.
	if m.Account != "" && password == "" {
		return c.signInWithAccount(ctx, env, m)
	}
	var auths []ssh.AuthMethod
	if password != "" {
		auths = append(auths, ssh.Password(password),
			// Some servers ask the password as a keyboard question instead.
			ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for i := range answers {
					answers[i] = password
				}
				return answers, nil
			}))
	}
	if len(auths) == 0 {
		return nil, fmt.Errorf("no password was given for %s", m.Name)
	}
	dialer := net.Dialer{Timeout: 15 * time.Second}
	address := net.JoinHostPort(m.Host, m.port())
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("%s does not answer on %s", m.Name, address)
	}
	// The host key is not pinned: these are machines on the home network, and a
	// known_hosts file here would be theatre.
	clientConn, chans, reqs, err := ssh.NewClientConn(conn, address, &ssh.ClientConfig{
		User: m.User, Auth: auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("the sign-in on %s failed", m.Name)
	}
	return ssh.NewClient(clientConn, chans, reqs), nil
}

// run is one command, with what it printed. A command that fails still gives
// back its output, because that output is usually the explanation.
func run(client *ssh.Client, command string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	var out, errOut bytes.Buffer
	session.Stdout = &out
	session.Stderr = &errOut
	err = session.Run(command)
	text := out.String()
	if strings.TrimSpace(errOut.String()) != "" {
		text += errOut.String()
	}
	return text, err
}

// wake sends the magic packet. Nothing answers it — the machine either comes up
// or it does not, which is why the view keeps asking afterwards.
func wake(m Machine) error {
	mac, err := hex.DecodeString(strings.NewReplacer(":", "", "-", "", ".", "").Replace(m.MAC))
	if err != nil || len(mac) != 6 {
		return fmt.Errorf("%q is not a MAC address", m.MAC)
	}
	packet := bytes.Repeat([]byte{0xff}, 6)
	for i := 0; i < 16; i++ {
		packet = append(packet, mac...)
	}
	broadcast := m.Broadcast
	if broadcast == "" {
		broadcast = "255.255.255.255"
	}
	conn, err := net.Dial("udp", net.JoinHostPort(broadcast, "9"))
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write(packet)
	return err
}

// signInWithAccount uses a key from the accounts menu. The key never leaves the
// server, and the person is not asked for anything.
func (c Capability) signInWithAccount(ctx context.Context, env *capability.Env, m Machine) (*ssh.Client, error) {
	accounts, err := env.Store.ListAccounts(ctx)
	if err != nil {
		return nil, err
	}
	var found *model.Account
	for i := range accounts {
		if accounts[i].Title == m.Account || accounts[i].ID.String() == m.Account {
			found = &accounts[i]
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("there is no account called %q", m.Account)
	}
	var settings map[string]string
	_ = json.Unmarshal(found.Config, &settings)
	user := m.User
	if user == "" {
		user = settings["user"]
	}

	var client *ssh.Client
	err = env.UseAccount(ctx, found.ID, func(secret []byte) error {
		signer, serr := parseKey(secret, settings["passphrase"])
		if serr != nil {
			return serr
		}
		address := net.JoinHostPort(m.Host, m.port())
		dialer := net.Dialer{Timeout: 15 * time.Second}
		conn, derr := dialer.DialContext(ctx, "tcp", address)
		if derr != nil {
			return fmt.Errorf("%s does not answer on %s", m.Name, address)
		}
		clientConn, chans, reqs, cerr := ssh.NewClientConn(conn, address, &ssh.ClientConfig{
			User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 15 * time.Second,
		})
		if cerr != nil {
			conn.Close()
			return fmt.Errorf("the key was refused by %s", m.Name)
		}
		client = ssh.NewClient(clientConn, chans, reqs)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return client, nil
}

func parseKey(secret []byte, passphrase string) (ssh.Signer, error) {
	if passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase(secret, []byte(passphrase))
	}
	return ssh.ParsePrivateKey(secret)
}
