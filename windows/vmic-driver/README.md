# vmic-driver — "Macawlink Microphone"

## Phase 1 (now): VB-Audio Virtual Cable
- User installs VB-CABLE (free/donationware; its licence does not allow us to bundle it).
- `internal/vmic` renders phone audio into **CABLE Input** via WASAPI shared mode.
- In Zoom/Teams the user selects **CABLE Output** as microphone.

## Phase 3: own driver
- Fork Microsoft's **SYSVAD** sample (Windows-driver-samples/audio/sysvad).
- Keep one hidden render endpoint (Go writes here) looped back to one capture endpoint
  named **"Macawlink Microphone"** (what apps see). Formats: 16/24/32/44.1/48 kHz, mono/stereo, 16-bit + float.
- Package as INF + CAT; sign with an **EV code-signing certificate** and submit for
  **attestation signing** in Microsoft Partner Center. Unsigned kernel drivers will not load
  on Windows 10/11 with Secure Boot.
- `internal/vmic.Targets` already prefers this endpoint when present, so no Go changes are needed.
