#!/usr/bin/env python3
"""Portable process ownership and installation locking for the local Azure host."""

import ctypes
import fcntl
import hashlib
import os
from pathlib import Path
import subprocess
import sys


class BSDProcessInfo(ctypes.Structure):
    _fields_ = [
        (name, ctypes.c_uint32) for name in (
            "flags", "status", "xstatus", "pid", "ppid", "uid", "gid",
            "ruid", "rgid", "svuid", "svgid", "reserved",
        )
    ] + [
        ("comm", ctypes.c_char * 16),
        ("name", ctypes.c_char * 32),
    ] + [
        (name, ctypes.c_uint32) for name in (
            "nfiles", "pgid", "pjobc", "tdev", "tpgid",
        )
    ] + [
        ("nice", ctypes.c_int32),
        ("start_seconds", ctypes.c_uint64),
        ("start_microseconds", ctypes.c_uint64),
    ]


def executable(pid):
    if sys.platform == "darwin":
        library = ctypes.CDLL("/usr/lib/libproc.dylib", use_errno=True)
        library.proc_pidpath.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.c_uint32]
        buffer = ctypes.create_string_buffer(4096)
        if library.proc_pidpath(pid, buffer, len(buffer)) <= 0:
            return ""
        return os.path.realpath(os.fsdecode(buffer.value))
    try:
        return os.path.realpath(os.readlink(f"/proc/{pid}/exe"))
    except (FileNotFoundError, PermissionError, ProcessLookupError):
        return ""


def identity(pid):
    if sys.platform == "darwin":
        library = ctypes.CDLL("/usr/lib/libproc.dylib", use_errno=True)
        library.proc_pidinfo.argtypes = [
            ctypes.c_int, ctypes.c_int, ctypes.c_uint64,
            ctypes.c_void_p, ctypes.c_int,
        ]
        info = BSDProcessInfo()
        # PROC_PIDTBSDINFO includes the kernel's microsecond start time, unlike ps.
        if library.proc_pidinfo(pid, 3, 0, ctypes.byref(info), ctypes.sizeof(info)) != ctypes.sizeof(info):
            return ""
        return f"{info.start_seconds}:{info.start_microseconds}"
    else:
        try:
            # comm can contain spaces and parentheses; fields after its final ')'
            # start at field 3, making starttime (field 22) index 19.
            return Path(f"/proc/{pid}/stat").read_text().rsplit(")", 1)[1].split()[19]
        except (FileNotFoundError, ProcessLookupError):
            return ""


def main():
    command, *args = sys.argv[1:]
    if command == "lock":
        path, *child = args
        with open(path, "a", encoding="utf-8") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            return subprocess.call(child)
    if command == "launch":
        directory, log, *child = args
        with open(log, "ab", buffering=0) as output:
            process = subprocess.Popen(
                child, stdin=subprocess.DEVNULL, stdout=output, stderr=output,
                cwd=directory, start_new_session=True, close_fds=True,
            )
        print(process.pid)
        return 0
    if command == "verify":
        path, expected = args
        digest = hashlib.sha256()
        with open(path, "rb") as archive:
            for block in iter(lambda: archive.read(1 << 20), b""):
                digest.update(block)
        return int(digest.hexdigest() != expected)
    if command == "identity":
        start = identity(int(args[0]))
        if not start:
            return 1
        print(start)
        return 0
    if command == "matches":
        pid, start, *expected = args
        if not pid or not start or identity(int(pid)) != start:
            return 1
        return int(bool(expected) and executable(int(pid)) != os.path.realpath(expected[0]))
    if command == "group":
        group, expected = args
        if not group:
            return 1
        processes = subprocess.check_output(["ps", "-axo", "pid=,pgid="], text=True)
        for line in processes.splitlines():
            pid, pgid = map(int, line.split())
            if pgid == int(group) and executable(pid) == os.path.realpath(expected):
                return 0
        return 1
    raise ValueError(f"unknown platform command: {command}")


if __name__ == "__main__":
    sys.exit(main())
