// Shared-memory layout between the Go receiver (internal/vcam) and the MF media source DLL.
// Change both sides together. Go fields are naturally aligned the same way on amd64.
#pragma once
#include <stdint.h>

#define MACAWLINK_SHM_NAME   L"Global\\Macawlink.Video"
#define MACAWLINK_EVENT_NAME L"Global\\Macawlink.Video.NewFrame"
#define MACAWLINK_SLOTS      3
#define MACAWLINK_MAX_W      3840
#define MACAWLINK_MAX_H      2160
#define MACAWLINK_SLOT_BYTES (MACAWLINK_MAX_W * MACAWLINK_MAX_H * 3 / 2)

typedef struct MacawlinkRingHeader {
    uint32_t magic;      // 'MKVC'
    uint32_t version;
    uint32_t latest;     // newest completed slot
    uint32_t _pad0;
    uint64_t seq;
    uint32_t width;
    uint32_t height;
    uint32_t stride;
    uint32_t fourcc;     // 'NV12'
    int64_t  timestamp;  // 100-ns units
    uint32_t connected;  // 0 => render "Waiting for phone…"
    uint32_t _pad1;
    // followed by MACAWLINK_SLOTS * MACAWLINK_SLOT_BYTES of NV12 data
} MacawlinkRingHeader;
