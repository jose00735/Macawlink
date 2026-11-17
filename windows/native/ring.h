// Shared-memory frame ring between the Go receiver (producer, internal/vcam/ring_windows.go)
// and the virtual camera media source in this DLL (consumer, running inside Frame Server).
// Layout is fixed little-endian x64; change both sides together and bump MK_RING_VERSION.
#pragma once
#include <stdint.h>

#define MK_RING_NAME    L"Global\\Macawlink.Video"
#define MK_RING_MAGIC   0x43564B4Du  // "MKVC"
#define MK_RING_VERSION 1u
#define MK_RING_SLOTS   3
#define MK_MAX_W        1920
#define MK_MAX_H        1080
#define MK_SLOT_BYTES   (MK_MAX_W * MK_MAX_H * 3 / 2)  // NV12
#define MK_HEADER_BYTES 256
#define MK_RING_BYTES   (MK_HEADER_BYTES + MK_RING_SLOTS * MK_SLOT_BYTES)

// Security descriptor for the mapping: Frame Server runs as LOCAL SERVICE in session 0,
// the Go app runs as the logged-on user, so grant both (and app containers) access.
#define MK_RING_SDDL L"D:P(A;;GA;;;WD)(A;;GA;;;SY)(A;;GA;;;LS)(A;;GA;;;AC)(A;;GA;;;S-1-15-2-2)S:(ML;;NW;;;LW)"

#pragma pack(push, 8)
typedef struct MkSlotMeta {
    uint64_t seq;        // equals header.seq at the time this slot was published
    uint32_t width;      // tight NV12: luma stride == width
    uint32_t height;
    int64_t  pts100ns;
} MkSlotMeta;            // 24 bytes

typedef struct MkRingHeader {
    uint32_t   magic;           // 0
    uint32_t   version;         // 4
    // ---- written by producer (Go) ----
    uint32_t   latest;          // 8   index of newest complete slot
    uint32_t   connected;       // 12  1 = phone streaming, 0 = show placeholder
    uint64_t   seq;             // 16  bumps after every publish
    MkSlotMeta slots[MK_RING_SLOTS]; // 24 .. 96
    // ---- written by consumer (DLL) ----
    uint32_t   reqWidth;        // 96  format the app opened (0 = nobody streaming)
    uint32_t   reqHeight;       // 100
    uint32_t   reqFps;          // 104
    uint32_t   _pad0;           // 108
    uint64_t   delivered;       // 112 frames handed to apps
    uint64_t   consumerTick;    // 120 GetTickCount64 of last delivery
    uint8_t    reserved[MK_HEADER_BYTES - 128];
} MkRingHeader;
#pragma pack(pop)

#ifdef __cplusplus
#include <stddef.h>
// Offsets pinned by windows/internal/vcam/vcam.go (offXxx constants).
static_assert(sizeof(MkRingHeader) == MK_HEADER_BYTES, "ring header size");
static_assert(offsetof(MkRingHeader, latest) == 8, "latest");
static_assert(offsetof(MkRingHeader, connected) == 12, "connected");
static_assert(offsetof(MkRingHeader, seq) == 16, "seq");
static_assert(offsetof(MkRingHeader, slots) == 24 && sizeof(MkSlotMeta) == 24, "slots");
static_assert(offsetof(MkRingHeader, reqWidth) == 96, "reqWidth");
static_assert(offsetof(MkRingHeader, reqFps) == 104, "reqFps");
static_assert(offsetof(MkRingHeader, delivered) == 112, "delivered");
static_assert(offsetof(MkRingHeader, consumerTick) == 120, "consumerTick");
#endif
