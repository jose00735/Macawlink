#!/usr/bin/env python3
"""Convert a compiled Windows resource file (.res) into a COFF object (.syso) that the Go
linker embeds automatically. Used to give macawlink.exe its icon, manifest and version info.

    python3 -m ziglang rc /fo macawlink.res macawlink.rc
    python3 res2syso.py macawlink.res ../../cmd/macawlink/rsrc_windows_amd64.syso
"""
import struct
import sys


def align(n, a=4):
    return (n + a - 1) & ~(a - 1)


def read_id(b, off):
    if struct.unpack_from("<H", b, off)[0] == 0xFFFF:
        return struct.unpack_from("<H", b, off + 2)[0], off + 4
    end = off
    while struct.unpack_from("<H", b, end)[0] != 0:
        end += 2
    return b[off:end].decode("utf-16-le").upper(), end + 2


def parse_res(b):
    off, out = 0, []
    while off + 8 <= len(b):
        data_size, header_size = struct.unpack_from("<II", b, off)
        typ, p = read_id(b, off + 8)
        name, p = read_id(b, p)
        p = align(p)
        _, _, lang, _, _ = struct.unpack_from("<IHHII", b, p)
        data = b[off + header_size: off + header_size + data_size]
        if not (typ == 0 and name == 0 and data_size == 0):
            out.append((typ, name, lang, data))
        off = align(off + header_size + data_size)
    return out


def build_rsrc(entries):
    """Returns (section bytes, list of offsets needing ADDR32NB relocations)."""
    tree = {}
    for typ, name, lang, data in entries:
        tree.setdefault(typ, {}).setdefault(name, {})[lang] = data

    def key(k):  # named entries first (sorted), then ids ascending
        return (0, k) if isinstance(k, str) else (1, k)

    # Layout pass: directories, then strings, then data entries, then data.
    dirs = []  # (node, level)

    def collect(node, level):
        dirs.append((node, level))
        if level < 2:
            for k in sorted(node, key=key):
                collect(node[k], level + 1)

    collect(tree, 0)
    dir_off, pos = {}, 0
    for node, level in dirs:
        dir_off[id(node)] = pos
        pos += 16 + 8 * len(node)
    strings, str_off = [], {}
    for node, level in dirs:
        for k in node:
            if isinstance(k, str) and k not in str_off:
                str_off[k] = pos
                enc = k.encode("utf-16-le")
                strings.append(struct.pack("<H", len(k)) + enc)
                pos += 2 + len(enc)
    pos = align(pos)
    leaves = []
    for node, level in dirs:
        if level == 2:
            for k in sorted(node, key=key):
                leaves.append((node, k))
    leaf_off = {}
    for node, k in leaves:
        leaf_off[(id(node), k)] = pos
        pos += 16
    data_off = {}
    for node, k in leaves:
        pos = align(pos, 8)
        data_off[(id(node), k)] = pos
        pos += len(node[k])
    buf = bytearray(align(pos, 8))
    relocs = []

    for node, level in dirs:
        o = dir_off[id(node)]
        names = [k for k in node if isinstance(k, str)]
        ids = [k for k in node if not isinstance(k, str)]
        struct.pack_into("<IIHHHH", buf, o, 0, 0, 0, 0, len(names), len(ids))
        e = o + 16
        for k in sorted(node, key=key):
            nm = (0x80000000 | str_off[k]) if isinstance(k, str) else k
            if level < 2:
                target = 0x80000000 | dir_off[id(node[k])]
            else:
                target = leaf_off[(id(node), k)]
            struct.pack_into("<II", buf, e, nm, target)
            e += 8
    p = min(str_off.values()) if str_off else 0
    for s in strings:
        buf[p:p + len(s)] = s
        p += len(s)
    for node, k in leaves:
        lo, do = leaf_off[(id(node), k)], data_off[(id(node), k)]
        struct.pack_into("<IIII", buf, lo, do, len(node[k]), 0, 0)  # OffsetToData fixed up by reloc
        relocs.append(lo)
        buf[do:do + len(node[k])] = node[k]
    return bytes(buf), relocs


def coff(section, relocs):
    hdr, sec_hdr = 20, 40
    raw = hdr + sec_hdr
    rel = raw + len(section)
    sym = rel + 10 * len(relocs)
    out = bytearray()
    out += struct.pack("<HHIIIHH", 0x8664, 1, 0, sym, 1, 0, 0)
    out += struct.pack("<8sIIIIIIHHI", b".rsrc", 0, 0, len(section), raw, rel, 0, len(relocs), 0, 0x40000040)
    out += section
    for r in relocs:
        out += struct.pack("<IIH", r, 0, 3)  # IMAGE_REL_AMD64_ADDR32NB against symbol 0
    out += struct.pack("<8sIhHBB", b".rsrc", 0, 1, 0, 3, 0)
    out += struct.pack("<I", 4)  # empty string table
    return bytes(out)


if __name__ == "__main__":
    res = open(sys.argv[1], "rb").read()
    section, relocs = build_rsrc(parse_res(res))
    open(sys.argv[2], "wb").write(coff(section, relocs))
    print(f"wrote {sys.argv[2]}: {len(section)} bytes of resources, {len(relocs)} entries")
