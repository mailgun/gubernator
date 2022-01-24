#! /usr/bin/env python

# Copyright 2018-2022 Mailgun Technologies Inc
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#


from gubernator import ratelimit_pb2 as pb
import gubernator
import argparse


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description='Gubernator CLI')

    parser.add_argument('--endpoint', '-e', action="store", dest="endpoint", default='127.0.0.1:9090')
    parser.add_argument('--timeout', '-t', action="store", dest="timeout", default=None)

    parser.add_argument('--namespace', '-n', action="store", dest="namespace", default="cli_ns")
    parser.add_argument('--key', '-k', action="store", dest="unique_key", default="cli_key")
    parser.add_argument('--hits', '-H', action="store", dest="hits", type=int, default=1)
    parser.add_argument('--duration', '-d', action="store", dest="duration", type=int, default=10000)
    parser.add_argument('--limit', '-l', action="store", dest="limit", type=int, default=5)

    opts = parser.parse_args()

    req = pb.Requests()
    rate_limit = req.requests.add()

    rate_limit.algorithm = pb.TOKEN_BUCKET
    rate_limit.duration = opts.duration
    rate_limit.limit = opts.limit
    rate_limit.namespace = opts.namespace
    rate_limit.unique_key = opts.unique_key
    rate_limit.hits = opts.hits

    client = gubernator.V1Client(endpoint=opts.endpoint)
    resp = client.GetRateLimits(req, timeout=opts.timeout)
    print(resp)
