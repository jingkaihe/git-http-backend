package main

import (
	"io"
	"os"
	"os/exec"
)

const gitBackend = "git"

// GitRPCClientConfig is the configuration for the Git RPC Service
type GitRPCClientConfig struct {
	Stream bool
}

// GitRPCClient is the stateless rpc client talks to Git
type GitRPCClient struct {
	RPCConfig    gitRPCConfig
	StdinWriter  io.WriteCloser
	StdoutReader io.ReadCloser
	StderrReader io.ReadCloser
	cmd          *exec.Cmd
	*GitRPCClientConfig
}

type gitRPCConfig map[string]string

// NewGitRPCClient returns a new GitRPCClient that works as a RPC client that
// talks to Git.
func NewGitRPCClient(config *GitRPCClientConfig) *GitRPCClient {
	cfg := make(gitRPCConfig)
	cfg["advertise_refs"] = "--advertise-refs"

	gs := &GitRPCClient{
		RPCConfig:          cfg,
		GitRPCClientConfig: config,
	}
	return gs
}

// Output is a block call that returns the RPC result back as a byte sequence
// It will return an error when the RPC call is not successful.
func (gs *GitRPCClient) Output() ([]byte, error) {
	return gs.cmd.Output()
}

// Wait happens after the Start call, which is a block call that will only finish
// when the RPC has been finished.
// Error will be raised when unexpected happens.
func (gs *GitRPCClient) Wait() error {
	return gs.cmd.Wait()
}

// Start begins a RPC call. It will expose the stdin/stdout/stderr pipe when
// streaming is allowed.
func (gs *GitRPCClient) Start() error {
	if gs.Stream {
		err := gs.ioPrepare()
		if err != nil {
			return err
		}
	}
	return gs.cmd.Start()
}

// UploadPack serves git fetch-pack and git ls-remote clients, which are
// invoked from git fetch, git pull, and git clone.
func (gs *GitRPCClient) UploadPack(repoPath string, cfg map[string]struct{}) {
	args := []string{"upload-pack"}

	for k := range cfg {
		args = append(args, gs.RPCConfig[k])
	}
	args = append(args, "--stateless-rpc", repoPath)

	gs.cmd = exec.Command(gitBackend, args...)
}

// ReceivePack serves git send-pack clients, which is invoked from git push.
func (gs *GitRPCClient) ReceivePack(repoPath string, cfg map[string]struct{}) {
	args := []string{"receive-pack"}

	for k := range cfg {
		args = append(args, gs.RPCConfig[k])
	}
	args = append(args, "--stateless-rpc", repoPath)

	gs.cmd = exec.Command(gitBackend, args...)
}

// UpdateServerInfo updates auxiliary info file to help dumb servers.
// It will update objects/info/packs and info/refs.
// See https://git-scm.com/docs/gitrepository-layout to understand what they are for
func (gs *GitRPCClient) UpdateServerInfo(repoPath string, cfg map[string]struct{}) {
	args := []string{"update-server-info"}

	for k := range cfg {
		args = append(args, gs.RPCConfig[k])
	}
	args = append(args, "--stateless-rpc", repoPath)

	pwd, _ := os.Getwd()
	defer os.Chdir(pwd)

	os.Chdir(repoPath)
	gs.cmd = exec.Command(gitBackend, args...)
}

func (gs *GitRPCClient) ioPrepare() error {
	var err error
	if gs.StdinWriter, err = gs.cmd.StdinPipe(); err != nil {
		return err
	}

	if gs.StdoutReader, err = gs.cmd.StdoutPipe(); err != nil {
		return err
	}

	if gs.StderrReader, err = gs.cmd.StderrPipe(); err != nil {
		return err
	}

	return nil
}
