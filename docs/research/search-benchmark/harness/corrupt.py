# usage: corrupt.py FILE FRACTION -- overwrites 4 KiB at FRACTION of FILE with fixed pseudo-random bytes
import os, random, sys
p = sys.argv[1]; frac = float(sys.argv[2]); n = os.path.getsize(p); off = int(n * frac)
random.seed(7)
with open(p, "r+b") as f:
    f.seek(off); f.write(bytes(random.randrange(256) for _ in range(4096)))
print("corrupted", os.path.basename(p), "size", n, "at", off)
