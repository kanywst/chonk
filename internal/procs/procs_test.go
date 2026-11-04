package procs

import (
	"context"
	"testing"

	"github.com/kanywst/chonk/internal/owner"
)

func TestListAndMatch(t *testing.T) {
	run := func(_ context.Context, name string, _ ...string) ([]byte, error) {
		switch name {
		case "ps":
			return []byte("  10  35232 /Applications/Rancher Desktop.app/Contents/Resources/resources/darwin/lima/bin/limactl\n" +
				"  11   1000 /opt/homebrew/bin/node\n" +
				"  12   2000 /bin/zsh\n" +
				"  13 1679440 /System/Library/Frameworks/Virtualization.framework/Versions/A/XPCServices/com.apple.Virtualization.VirtualMachine.xpc/Contents/MacOS/com.apple.Virtualization.VirtualMachine\n" +
				" bad line\n"), nil
		case "lsof":
			return []byte("p10\nfcwd\nn/\np11\nfcwd\nn/h/proj/web\np12\nfcwd\nn/h\n"), nil
		}
		return nil, nil
	}
	ps, err := List(context.Background(), run)
	if err != nil || len(ps) != 4 {
		t.Fatalf("List = %v %v", ps, err)
	}
	if ps[0].Exe != "/Applications/Rancher Desktop.app/Contents/Resources/resources/darwin/lima/bin/limactl" || ps[0].RSS != 35232*1024 || ps[1].Cwd != "/h/proj/web" {
		t.Errorf("parsed = %+v", ps)
	}

	vm := &owner.Item{Owner: &owner.Owner{ID: "rancher-desktop", Kind: owner.KindVM, Procs: []string{"Rancher Desktop.app/Contents/Resources/resources/darwin/lima"}}, Path: "/h/Library/rd/lima"}
	brew := &owner.Item{Owner: &owner.Owner{ID: "homebrew", Kind: owner.KindToolchain}, Path: "/opt/homebrew"}
	proj := &owner.Item{Owner: &owner.Owner{ID: "project", Kind: owner.KindProject}, Path: "/h/proj"}
	got := Match([]*owner.Item{vm, brew, proj}, ps, "/h")
	if len(got[vm]) != 2 || got[vm][0].PID != 13 {
		t.Errorf("vm = %v", got[vm])
	}
	// node's executable is under Homebrew, which wins over its cwd.
	if len(got[brew]) != 1 || len(got[proj]) != 0 {
		t.Errorf("brew = %v proj = %v", got[brew], got[proj])
	}
	if Total(got[vm]) != (35232+1679440)*1024 {
		t.Error("total")
	}
}
