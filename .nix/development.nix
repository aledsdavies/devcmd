# Development environment for devcmd project - smart derivation approach
{ pkgs, self ? null, gitRev ? "dev", system }:
let
  # Import our library to create the development CLI using fixed-output derivation
  devcmdLib = import ./lib.nix {
    inherit pkgs self gitRev system;
    lib = pkgs.lib;
  };

  # Create a shell script that generates the dev CLI on demand
  devCLI = devcmdLib.mkDevCLI {
    name = "devcmd-dev-cli";
    binaryName = "dev";
    commandsFile = ../commands.cli;
    version = "dev-${gitRev}";
  };
in
pkgs.mkShell {
  name = "devcmd-dev";

  buildInputs = with pkgs; [
    # Development tools
    go
    gopls
    golangci-lint
    git
    zsh
    nixpkgs-fmt
    gofumpt
  ] ++ [
    self.packages.${system}.devcmd # Include the devcmd binary itself
    devCLI # Include the generated dev CLI
  ];

  shellHook = ''
    echo "🔧 Devcmd Development Environment"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    echo "Available commands:"
    echo "  devcmd - The devcmd CLI generator"
    echo "  dev    - Development commands for this project"
    echo ""
    echo "Run 'dev help' to see available development commands"
    exec ${pkgs.zsh}/bin/zsh
  '';
}
