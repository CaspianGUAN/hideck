# Embedded module voice runtime

`assets/` contains the pinned `qdc507-3.18.44-voice-20260712.5` runtime
from MaVo commit `0443dfdaf8aec086fd76ba2ee9152fd908114524`, directory
`Resources/ModuleVoice`. Source URL and all six sizes/SHA-256 digests are
recorded in `bundle.go`.

The original `COPYING-GPL-2.0`, `MODULE-REPORT.md`, and `manifest.json` are
distributed unchanged alongside the binaries. `TestEmbeddedBundleMatchesPinnedArtifacts`
verifies these files, including the metadata and license.

Go embeds these files in the host executable. Preparation reads the embedded
filesystem and verifies every digest before upload. It does not access GitHub
or `data/modem-voice/bundles`. The old cache is not deleted automatically.
Host-side `adb`, `arecord` and `aplay` are separate system dependencies, not
part of this module-side ARM runtime.
