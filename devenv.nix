{ pkgs, ... }:
{
  # sandbox-runtime is srt itself. On Linux it carries its own bubblewrap,
  # socat and ripgrep, so a checkout can build and run srtbox with nothing else.
  languages.go.enable = true;
  packages = [ pkgs.gopls pkgs.sandbox-runtime ];
}
