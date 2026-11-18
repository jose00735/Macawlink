#include "ring_reader.h"
#include <sddl.h>

bool MkRingReader::Open() {
    if (ring_) return true;
    ULONGLONG now = GetTickCount64();
    if (lastTry_ && now - lastTry_ < 1000) return false;
    lastTry_ = now;

    bool existed = false;
    if (scope_ == kGlobalFirst) {
        PSECURITY_DESCRIPTOR sd = nullptr;
        SECURITY_ATTRIBUTES sa = {sizeof(sa), nullptr, FALSE};
        if (ConvertStringSecurityDescriptorToSecurityDescriptorW(MK_RING_SDDL, SDDL_REVISION_1, &sd, nullptr))
            sa.lpSecurityDescriptor = sd;
        map_ = CreateFileMappingW(INVALID_HANDLE_VALUE, sd ? &sa : nullptr, PAGE_READWRITE, 0, MK_RING_BYTES, MK_RING_NAME);
        existed = GetLastError() == ERROR_ALREADY_EXISTS;
        if (sd) LocalFree(sd);
        if (!map_) {
            map_ = OpenFileMappingW(FILE_MAP_ALL_ACCESS, FALSE, MK_RING_NAME);
            existed = true;
        }
    }
    if (!map_) {
        // Session-local name: the Go receiver creates it at startup.
        map_ = OpenFileMappingW(FILE_MAP_ALL_ACCESS, FALSE, MK_RING_NAME_LOCAL);
        existed = map_ != nullptr;
        if (!map_) {
            map_ = CreateFileMappingW(INVALID_HANDLE_VALUE, nullptr, PAGE_READWRITE, 0, MK_RING_BYTES, MK_RING_NAME_LOCAL);
            existed = GetLastError() == ERROR_ALREADY_EXISTS;
        }
    }
    if (!map_) {
        MkLog("ring: unavailable (err %lu)", GetLastError());
        return false;
    }
    ring_ = (BYTE*)MapViewOfFile(map_, FILE_MAP_ALL_ACCESS, 0, 0, MK_RING_BYTES);
    if (!ring_) { CloseHandle(map_); map_ = nullptr; return false; }
    auto* hd = reinterpret_cast<MkRingHeader*>(ring_);
    if (!existed || hd->magic != MK_RING_MAGIC || hd->version != MK_RING_VERSION) {
        memset(hd, 0, sizeof(*hd));
        hd->version = MK_RING_VERSION;
        hd->magic = MK_RING_MAGIC;
    }
    MkLog("ring: mapped (%s, existed=%d)", scope_ == kGlobalFirst ? "global-first" : "local", existed);
    return true;
}

void MkRingReader::Close() {
    if (ring_) {
        SetRequested(0, 0, 0);
        UnmapViewOfFile(ring_);
        ring_ = nullptr;
    }
    if (map_) { CloseHandle(map_); map_ = nullptr; }
}

void MkRingReader::SetRequested(UINT32 w, UINT32 h, UINT32 fps) {
    if (!ring_) return;
    auto* hd = reinterpret_cast<volatile MkRingHeader*>(ring_);
    hd->reqWidth = w;
    hd->reqHeight = h;
    hd->reqFps = fps;
}

void MkRingReader::Fill(BYTE* y, LONG pitch, BYTE* uv, UINT32 w, UINT32 h, uint64_t frameNo) {
    if (!ring_) Open();
    if (ring_) {
        auto* hd = reinterpret_cast<volatile MkRingHeader*>(ring_);
        hd->reqWidth = w;
        hd->reqHeight = h;
        hd->consumerTick = GetTickCount64();
        if (hd->connected && hd->seq != 0 && hd->magic == MK_RING_MAGIC && hd->version == MK_RING_VERSION) {
            MemoryBarrier();
            uint32_t idx = hd->latest % MK_RING_SLOTS;
            uint32_t sw = hd->slots[idx].width, sh = hd->slots[idx].height;
            if (sw && sh && sw <= MK_MAX_W && sh <= MK_MAX_H && !(sw & 1) && !(sh & 1)) {
                const BYTE* src = ring_ + MK_HEADER_BYTES + (size_t)idx * MK_SLOT_BYTES;
                const BYTE* suv = src + (size_t)sw * sh;
                if (sw == w && sh == h) {
                    for (UINT32 r = 0; r < h; r++) memcpy(y + (size_t)r * pitch, src + (size_t)r * w, w);
                    for (UINT32 r = 0; r < h / 2; r++) memcpy(uv + (size_t)r * pitch, suv + (size_t)r * w, w);
                } else {
                    // Format switch in flight: nearest-neighbour until Go rescales to the new size.
                    for (UINT32 r = 0; r < h; r++) {
                        const BYTE* srow = src + (size_t)(r * sh / h) * sw;
                        BYTE* drow = y + (size_t)r * pitch;
                        for (UINT32 c = 0; c < w; c++) drow[c] = srow[c * sw / w];
                    }
                    for (UINT32 r = 0; r < h / 2; r++) {
                        const BYTE* srow = suv + (size_t)(r * (sh / 2) / (h / 2)) * sw;
                        BYTE* drow = uv + (size_t)r * pitch;
                        for (UINT32 c = 0; c < w / 2; c++) {
                            UINT32 sc = (c * (sw / 2) / (w / 2)) * 2;
                            drow[c * 2] = srow[sc];
                            drow[c * 2 + 1] = srow[sc + 1];
                        }
                    }
                }
                hd->delivered = hd->delivered + 1;
                return;
            }
        }
    }
    MkDrawWaitingCard(y, pitch, uv, w, h, frameNo);
}

void MkDrawWaitingCard(BYTE* y, LONG pitch, BYTE* uv, UINT32 w, UINT32 h, uint64_t frameNo) {
    const BYTE bgY = 25, bgU = 140, bgV = 129;   // #0D0522
    const BYTE cyY = 162, cyU = 170, cyV = 22;   // #00F0FF
    const BYTE mgY = 124, mgU = 172, mgV = 209;  // #FF2BD6
    UINT32 band = h / 60 ? h / 60 : 1;
    UINT32 scan = (UINT32)((frameNo * 6) % h);
    UINT32 boxW = w / 6, boxH = h / 6, bx = (w - boxW) / 2, by = (h - boxH) / 2;
    UINT32 t = h / 180 ? h / 180 : 1;
    for (UINT32 r = 0; r < h; r++) {
        BYTE* row = y + (size_t)r * pitch;
        bool inScan = r >= scan && r < scan + band;
        memset(row, inScan ? cyY : bgY, w);
        if (!inScan && r >= by && r < by + boxH) {
            bool edge = r < by + t || r >= by + boxH - t;
            if (edge) memset(row + bx, mgY, boxW);
            else { memset(row + bx, mgY, t); memset(row + bx + boxW - t, mgY, t); }
        }
    }
    for (UINT32 r = 0; r < h / 2; r++) {
        BYTE* row = uv + (size_t)r * pitch;
        bool inScan = r * 2 >= scan && r * 2 < scan + band;
        bool inBox = r * 2 >= by && r * 2 < by + boxH;
        bool edge = r * 2 < by + t || r * 2 >= by + boxH - t;
        for (UINT32 c = 0; c < w / 2; c++) {
            BYTE u = bgU, v = bgV;
            UINT32 px = c * 2;
            if (inScan) { u = cyU; v = cyV; }
            else if (inBox && px >= bx && px < bx + boxW && (edge || px < bx + t || px >= bx + boxW - t)) { u = mgU; v = mgV; }
            row[c * 2] = u;
            row[c * 2 + 1] = v;
        }
    }
}
