# Copyright 2018-2019 Mailgun Technologies Inc
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

import pytest
import subprocess
import os
import gubernator


@pytest.fixture(scope='module')
def cluster():
    args = ["/bin/sh", "-c",
            "go run ./cmd/gubernator-cluster/main.go"]

    os.chdir("golang")
    proc = subprocess.Popen(args, stdout=subprocess.PIPE)
    os.chdir("..")

    while True:
        line = proc.stdout.readline()
        if b'Ready' in line:
            break
    yield proc
    proc.kill()


def test_health_check(cluster):
    client = gubernator.V1Client()
    resp = client.health_check()
    print("Health:", resp)


def test_get_rate_limit(cluster):
    req = pb.Requests()
    rate_limit = req.requests.add()

    rate_limit.algorithm = pb.TOKEN_BUCKET
    rate_limit.duration = gubernator.SECOND * 2
    rate_limit.limit = 10
    rate_limit.namespace = 'test-ns'
    rate_limit.unique_key = 'domain-id-0001'
    rate_limit.hits = 1

    client = gubernator.V1Client()
    resp = client.GetRateLimits(req, timeout=0.5)
    print("RateLimit: {}".format(resp))
