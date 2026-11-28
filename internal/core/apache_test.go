package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApacheStartUsesPreConfigDefineFlags(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Darius DB Test")
	p := NewPaths(root)
	instDir := filepath.Join(p.EngineBin("apache"), "apache-2.4.71")
	if err := os.MkdirAll(filepath.Join(instDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(instDir, "bin", "httpd.exe"), []byte("MZstub"), 0o644); err != nil {
		t.Fatal(err)
	}

	eng := &apacheEngine{}
	inst := Installation{Engine: "apache", Name: "apache-2.4.71", Version: "2.4.71", Dir: instDir}
	es := EngineSettings{Port: 8080}

	if err := eng.Prepare(context.Background(), p, inst, es, nil); err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if !dirExists(p.Www) || !dirExists(p.Logs) {
		t.Fatalf("expected www and logs directories to exist after Prepare")
	}

	spec, err := eng.Start(p, inst, es)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	args := spec.Cmd.Args[1:]

	// Every Define directive MUST be preceded by uppercase -C (processed BEFORE httpd.conf),
	// never lowercase -c (which runs after httpd.conf and causes AH00111 / AH00526).
	definesSeen := map[string]bool{}
	for i := 0; i < len(args)-1; i++ {
		if strings.HasPrefix(args[i+1], "Define ") {
			if args[i] != "-C" {
				t.Fatalf("expected -C before %q, got %q (full args: %v)", args[i+1], args[i], args)
			}
			fields := strings.Fields(args[i+1])
			if len(fields) >= 2 {
				definesSeen[fields[1]] = true
			}
		}
	}
	for _, want := range []string{"SRVROOT", "WWWROOT", "LOGROOT"} {
		if !definesSeen[want] {
			t.Fatalf("missing -C Define %s in apache args: %v", want, args)
		}
	}

	// If a real local Apache 2.4 installation exists on this PC, run httpd.exe -t with the
	// exact arguments constructed by Start() to verify Windows quoting & zero AH00111/AH00526 errors.
	realApacheDir := `C:\DariusDB\bin\apache\apache-2.4.71`
	if fileExists(filepath.Join(realApacheDir, "bin", "httpd.exe")) {
		realInst := Installation{Engine: "apache", Name: "apache-2.4.71", Version: "2.4.71", Dir: realApacheDir}
		realSpec, err := eng.Start(p, realInst, es)
		if err != nil {
			t.Fatalf("Start with real apache dir failed: %v", err)
		}
		testArgs := make([]string, 0, len(realSpec.Cmd.Args)-1)
		for _, a := range realSpec.Cmd.Args[1:] {
			if a == "-DFOREGROUND" {
				testArgs = append(testArgs, "-t")
			} else {
				testArgs = append(testArgs, a)
			}
		}
		checkCmd := exec.Command(realSpec.Cmd.Path, testArgs...)
		checkCmd.Dir = realSpec.Cmd.Dir
		out, err := checkCmd.CombinedOutput()
		if err != nil {
			t.Fatalf("httpd.exe -t failed: %v\noutput:\n%s", err, string(out))
		}
		if strings.Contains(string(out), "AH00111") || strings.Contains(string(out), "AH00526") {
			t.Fatalf("unexpected Apache config variable warning/error:\n%s", string(out))
		}
	}
}
