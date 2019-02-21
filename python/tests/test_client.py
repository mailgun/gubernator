import pytest
import subprocess
import os

from gubernator.client import Client, SECOND


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
    client = Client()
    resp = client.health_check()
    print("Health:", resp)


def test_get_rate_limit(cluster):
    client = Client()
    resp = client.get_rate_limit("test-ns", "domain_id_00001", 10,
                                 SECOND*2, hits=1)
    print("RateLimit: {}".format(resp))
