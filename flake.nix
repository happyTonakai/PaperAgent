{
  description = "PaperAgent – AI paper reading assistant";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      supportedSystems = [ "x86_64-linux" "aarch64-darwin" "x86_64-darwin" ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      packages = forAllSystems (system:
        let
          pkgs = import nixpkgs { inherit system; };
          lib = pkgs.lib;

          version = "1.1.1";

          src = pkgs.fetchFromGitHub {
            owner = "RicherMans";
            repo = "PaperAgent";
            rev = "8fa1deab4380e5e39fab14c3c5cd0e1225c33b3c";
            hash = "sha256-G0mCfWgDGorVOroWXQf3Jpz15nuvRYkdAnNppSXTBKs=";
          };

          frontend = pkgs.buildNpmPackage {
            pname = "paperagent-frontend";
            inherit version src;
            sourceRoot = "source/frontend";
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
          paperagent = pkgs.buildGoModule {
            pname = "paperagent";
            inherit version src;

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

          default = self.packages.${system}.paperagent;
        }
      );

      overlays.default = final: prev: {
        paperagent = self.packages.${prev.system}.paperagent or null;
      };
    };
}
