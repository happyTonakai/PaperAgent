{
  description = "PaperAgent – AI paper reading assistant";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-parts.url = "github:hercules-ci/flake-parts";
  };

  outputs = inputs@{ self, nixpkgs, flake-parts, ... }:
    flake-parts.lib.mkFlake { inherit inputs; } {
      systems = [ "x86_64-linux" "aarch64-darwin" "x86_64-darwin" ];

      perSystem = { config, self', inputs', pkgs, system, lib, ... }:
        let
          supportedSystems = [ "x86_64-linux" "aarch64-darwin" "x86_64-darwin" ];
          version = "1.1.1";

          frontendSrc = builtins.path {
            path = ./frontend;
            name = "frontend-src";
            filter = path: type:
              baseNameOf path != "node_modules"
              && baseNameOf path != ".git";
          };

          frontend = pkgs.buildNpmPackage {
            pname = "paperagent-frontend";
            inherit version;
            src = frontendSrc;
            sourceRoot = "frontend-src";
            npmDepsHash = "sha256-sE/EqqiuhPBFBoGMue+AQn7pX8XTncmrLSOl4ZQP5jU=";

            postPatch = ''
              substituteInPlace vite.config.ts \
                --replace-fail "outDir: '../internal/server/frontend-dist'" "outDir: './dist'"
            '';

            installPhase = ''
              mkdir -p $out
              cp -r dist/* $out/
            '';
          };

          isLinux = lib.hasSuffix "linux" system;
          isDarwin = lib.hasSuffix "darwin" system;
        in
        {
          packages = {
            paperagent = pkgs.buildGoModule {
              pname = "paperagent";
              inherit version;
              src = ./.;

              vendorHash = "sha256-2oe+pRvFX/gO8mScMdso7pnPbnMPxrorQBZvPLaOniA=";

              nativeBuildInputs = [ pkgs.pkg-config ]
                ++ lib.optionals isLinux [ pkgs.gtk3 ];

              buildInputs = lib.optionals isLinux [
                pkgs.gtk3
                pkgs.glib
                pkgs.libayatana-appindicator
              ] ++ lib.optionals isDarwin [
                pkgs.darwin.apple_sdk.frameworks.AppKit
              ];

              postPatch = ''
                substituteInPlace go.mod \
                  --replace-fail 'go 1.25.8' 'go ${lib.versions.majorMinor pkgs.go.version}'
              '';

              preBuild = ''
                rm -rf internal/server/frontend-dist
                cp -r ${frontend} internal/server/frontend-dist
                chmod -R u+w internal/server/frontend-dist
              '';

              ldflags = [ "-s" "-w" "-X main.version=${version}" ];

              doCheck = false;

              env.CGO_ENABLED = "1";

              meta = with lib; {
                description = "AI paper reading assistant – paste an arXiv link, get deep analysis with multi-turn Q&A";
                homepage = "https://github.com/RicherMans/PaperAgent";
                license = licenses.mit;
                mainProgram = "paperagent";
                platforms = supportedSystems;
              };
            };

            default = self'.packages.paperagent;
          };
        };

      flake = {
        overlays.default = final: prev: {
          paperagent = self.packages.${prev.system}.paperagent or null;
        };
      };
    };
}
