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
