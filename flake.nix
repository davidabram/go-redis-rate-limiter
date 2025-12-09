{
  description = "Redis Rate Limiter - Go development environment with Redis";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            gotools
            go-tools
            gopls

            redis

            delve
            air
          ];

          shellHook = ''
            echo "Go development environment loaded"
            echo "Go version: $(go version)"
            echo "Redis version: $(redis-server --version)"
            echo ""
            echo "Available commands:"
            echo "  go        - Go toolchain"
            echo "  redis-server - Start Redis server"
            echo "  redis-cli    - Redis CLI client"
          '';
        };
      }
    );
}
