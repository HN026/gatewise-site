package benchmark

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type process struct {
	cmd      *exec.Cmd
	log      *os.File
	done     chan error
	resource Resource
	mu       sync.Mutex
}

func startProcess(ctx context.Context, dir, role, binary string, args ...string) (*process, error) {
	log, err := os.Create(filepath.Join(dir, role+".log"))
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binary, args...)
	// Reproducible configs must not inherit the developer's Gatewise overrides.
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GATEWISE_") {
			cmd.Env = append(cmd.Env, value)
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Close()
		return nil, err
	}
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, err
	}
	p := &process{cmd: cmd, log: log, done: make(chan error, 1), resource: Resource{Role: role, PID: cmd.Process.Pid}}
	go func() { _, _ = io.Copy(log, stdout); p.done <- cmd.Wait() }()
	return p, nil
}

func (p *process) stop() Resource {
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
	if state := p.cmd.ProcessState; state != nil {
		p.resource.CPUSeconds = (state.UserTime() + state.SystemTime()).Seconds()
	}
	p.log.Close()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.resource
}

func sampleRSS(pids []int) map[int]int64 {
	ids := make([]string, 0, len(pids))
	for _, pid := range pids {
		ids = append(ids, strconv.Itoa(pid))
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-o", "pid=,rss=", "-p", strings.Join(ids, ",")).Output()
	result := map[int]int64{}
	if err != nil {
		return result
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		var pid int
		var rss int64
		if _, err := fmt.Sscan(scanner.Text(), &pid, &rss); err == nil {
			result[pid] = rss
		}
	}
	return result
}

func monitor(ctx context.Context, processes []*process, path string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f, err := os.Create(path)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintln(f, "unix_ms,role,pid,rss_kib")
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		pids := []int{os.Getpid()}
		for _, p := range processes {
			pids = append(pids, p.cmd.Process.Pid)
		}
		for {
			values := sampleRSS(pids)
			for _, p := range processes {
				v := values[p.cmd.Process.Pid]
				p.mu.Lock()
				if v > p.resource.ObservedPeakRSSKB {
					p.resource.ObservedPeakRSSKB = v
				}
				p.mu.Unlock()
				fmt.Fprintf(f, "%d,%s,%d,%d\n", time.Now().UnixMilli(), p.resource.Role, p.cmd.Process.Pid, v)
			}
			fmt.Fprintf(f, "%d,generator,%d,%d\n", time.Now().UnixMilli(), os.Getpid(), values[os.Getpid()])
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return done
}

func waitReady(ctx context.Context, client *http.Client, url string) error {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		r, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(r)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("service not ready at %s", url)
		case <-tick.C:
		}
	}
}
