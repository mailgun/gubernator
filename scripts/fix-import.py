#! /usr/bin/env python

from __future__ import print_function

import fileinput
import re

regex = re.compile("import.*_pb2")

files = [
    "python/gubernator/ratelimit_pb2_grpc.py"
]

for file in files:
    for line in fileinput.input(file, inplace=True):
        if regex.match(line):
            print("from . " + line, end='')
            continue
        print(line, end='')
