#pragma once
#include "common.h"
#include "ring.h"

// Consumer side of the shared frame ring, used by both camera front-ends:
// the Windows 11 Media Foundation source (in Frame Server) and the DirectShow filter
// (inside the app's process on Windows 10).
class MkRingReader {
public:
    enum Scope {
        kGlobalFirst,  // Frame Server: create Global\ (has the privilege), fall back to Local\.
        kLocalOnly,    // In-app DirectShow filter: Local\ in the user's session (Go creates it).
    };
    explicit MkRingReader(Scope scope) : scope_(scope) {}
    ~MkRingReader() { Close(); }

    bool Open();  // retries at most once per second
    void Close();
    bool IsOpen() const { return ring_ != nullptr; }

    // Advertise which format an app opened (0 = stopped) so the producer scales for it.
    void SetRequested(UINT32 w, UINT32 h, UINT32 fps);

    // Writes one NV12 frame: the phone's latest picture, or the "waiting" card.
    void Fill(BYTE* y, LONG pitch, BYTE* uv, UINT32 w, UINT32 h, uint64_t frameNo);

private:
    Scope scope_;
    HANDLE map_ = nullptr;
    BYTE* ring_ = nullptr;
    ULONGLONG lastTry_ = 0;
};

// Placeholder card in the Macawlink palette (deep violet + cyan scan line + magenta frame).
void MkDrawWaitingCard(BYTE* y, LONG pitch, BYTE* uv, UINT32 w, UINT32 h, uint64_t frameNo);
