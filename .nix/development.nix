# Development environment for devcmd project
# Dogfooding our own tool for development commands
{ pkgs, self ? null, gitRev ? "dev", system }:
let
  # Import our own library to create the development CLI
  devcmdLib = import ./lib.nix {
    inherit pkgs self gitRev system;
    lib = pkgs.lib;
  };

  # Generate the development CLI from our commands.cli file - fail if can't build
  devCLI = devcmdLib.mkDevCLI
    {
      name = "dev";
      binaryName = "dev"; # Explicitly set binary name for self-awareness
      commandsFile = ../commands.cli;
      version = "latest";
    };
in
pkgs.mkShell {
  name = "devcmd-dev";
  buildInputs = with pkgs; [
    # Core Go development
    go
    gopls
    golangci-lint
    # Development tools
    git
    zsh
    # Code formatting
    nixpkgs-fmt
    gofumpt
  ] ++ [ devCLI ];
  shellHook = ''
    echo "🔧 Devcmd Development Environment"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    dev help
    exec ${pkgs.zsh}/bin/zsh
  '';
}
