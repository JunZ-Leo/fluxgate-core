{
  description = "Fluxgate Core, a compatibility-first proxy core";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/master";

  inputs.utils.url = "github:numtide/flake-utils";

  outputs = { self, nixpkgs, utils }:
    utils.lib.eachDefaultSystem
      (system:
        let
          pkgs = import nixpkgs {
            inherit system;
            overlays = [ self.overlay ];
          };
        in
        rec {
          packages.fluxgate = pkgs.fluxgate;
          packages.default = packages.fluxgate;
        }
      ) //
    (
      let version = nixpkgs.lib.substring 0 8 self.lastModifiedDate or self.lastModified or "19700101"; in
      {
        overlay = final: prev: {

          fluxgate = final.buildGo119Module {
            pname = "fluxgate";
            inherit version;
            src = ./.;

            vendorSha256 = "sha256-W5oiPtTRin0731QQWr98xZ2Vpk97HYcBtKoi1OKZz+w=";

            # Do not build testing suit
            excludedPackages = [ "./test" ];

            CGO_ENABLED = 0;

            ldflags = [
              "-s"
              "-w"
              "-X github.com/metacubex/mihomo/constant.Version=dev-${version}"
              "-X github.com/metacubex/mihomo/constant.BuildTime=${version}"
            ];
            
            tags = [
              "with_gvisor"
            ];

            # Network required 
            doCheck = false;

            postInstall = ''
              mv $out/bin/mihomo $out/bin/fluxgate
            '';

          };
        };
      }
    );
}
