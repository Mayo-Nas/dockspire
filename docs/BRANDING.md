# Dockspire branding

The user-supplied `Dockspire_Brand_Pack.zip` replaces the previous working name, Stacklane, with **Dockspire**. The approved direction is the flat orbital docking station: central blue-and-white spire, segmented docking ring, and spacecraft approaching from the left. The tagline is **DOCK. DEPLOY. MANAGE.**

The [asset status](branding/ASSET_LICENSE_NOTES.md) records the supplied artwork’s provenance and limitations. Runtime images are copied directly from the pack without tracing, recoloring, or modifying the motif.

## Application use

- Login: supplied dark horizontal logo, including the original wordmark and tagline.
- Sidebar: supplied dark app icon at 44 px (40 px on narrow screens), with a responsive text wordmark. `Dock` is white and `spire` is blue. This uses the existing system font because the package includes no font files.
- Browser: supplied multi-size ICO favicon and 256 px touch icon. The sidebar uses 64/128 px PNGs for standard/high-density displays.
- README: matching supplied dark/light horizontal logos.

Only the exact runtime logo/icon paths, stylesheet, login assets, and minimal health endpoint are public before sign-in. Application pages, scripts, inventory, definitions, and other brand-directory paths retain their existing authentication requirements. Assets are embedded in the Go executable and require no external font/image service or frontend build.

## Interface palette

| Role | Color |
| --- | --- |
| Brand navy / browser theme | `#0B1F3B` |
| App background | `#0B1422` |
| Sidebar | `#0C1B2C` |
| Panels | `#121F30` |
| Electric blue / primary actions / charts | `#2EA0FF` |
| Primary action hover | `#66BAFF` |
| Active navigation / selected tabs | `#142F4B` |
| Main text | `#EDF5FF` |
| Secondary text | `#A0B1C6` |

These UI choices interpret the suggested palette against the supplied source; they are not claimed to be exact original artwork specifications. Green success/running, amber warning/partial, and red error/unhealthy indicators keep their functional meaning. Keyboard focus uses a visible blue outline. The interface retains its existing dark theme; the supplied light logo is used in documentation.

## Compatibility

Visible interface copy, errors, startup messages, documentation, and browser download names use Dockspire. Download filenames are now `dockspire-library-<timestamp>.zip` and `dockspire-<stack>-compose.zip`.

Existing deployment and storage contracts keep their legacy identifiers: `stacklane` executable/release paths, `stacklane-preview` Compose project/image names, `STACKLANE_*` environment variables, host-helper service/socket paths, cookie names, `X-Stacklane-CSRF`, archive manifest kind `stacklane-library`, and exported `stacklane-record.json`. This avoids creating a second installation or invalidating saved libraries during a visual rebrand. Existing archives still restore without conversion. A future operational rename needs a separate migration plan. The later source reorganization changed only the Go module to `github.com/Mayo-Nas/dockspire`; that build identifier does not migrate runtime state.

Dockspire currently focuses on Compose stacks. Multi-host management is planned; the branding does not imply that unimplemented features are available.

## Asset limitations

The supplied artwork consists of raster crops with original backgrounds and possible edge/shadow artifacts. There is no vector master or bundled font. The original ICO remains the favicon prototype; any future vector redraw or simplified small-size mark should be reviewed against the approved concept. See the original asset notes for provenance; no trademark or domain clearance is represented.
