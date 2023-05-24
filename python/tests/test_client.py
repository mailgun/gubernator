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

import os
import pytest
import subprocess

import grpc

import gubernator
from gubernator import Algorithm, GetRateLimitsReq, V1Stub

SECOND = 1000


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
    with grpc.insecure_channel("127.0.0.1:9090") as channel:
        client = V1Stub(channel)
        resp = client.HealthCheck()
        print("Health:", resp)


def test_get_rate_limit(cluster):
    req = GetRateLimitsReq(
        algorithm=Algorithm.TOKEN_BUCKET,
        duration=2 * SECOND,
        hits=1,
        limit=10,
        name="test-ns",
        unique_key="domain-id-0001"
    )

    with grpc.insecure_channel("127.0.0.1:9090") as channel:
        client = V1Stub(channel)
        resp = client.GetRateLimits(req, timeout=0.5)
        print("RateLimit:", resp)
