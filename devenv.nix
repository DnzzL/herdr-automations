{ pkgs, ... }:

{
  # Same Go as CI (.github/workflows/ci.yml); gofmt ships with it.
  # Not languages.go: it pulls a gopls that fails to build against go_1_26.
  # tmux, asciinema, agg, python3: scripts/demo/record*.sh (docs/*.gif).
  packages = with pkgs; [
    go_1_26
    tmux
    asciinema
    asciinema-agg
    python3
  ];

  # Never let go.mod download another toolchain behind the pinned one.
  env.GOTOOLCHAIN = "local";

  # Mirrors CI, so a green `ci` here means a green PR.
  scripts.ci.exec = ''
    set -e
    go build ./...
    go vet ./...
    go test ./...
    test -z "$(gofmt -l .)" || { gofmt -l .; exit 1; }
  '';

  enterTest = "ci";
}
