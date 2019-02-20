import pytest
import subprocess
import os

from gubernator.client import Client


@pytest.fixture(scope='module')
def cluster():
    args = ["/bin/sh", "-c",
            "go run ./cmd/gubernator-cluster/main.go"]

    os.chdir("../golang")
    proc = subprocess.Popen(args, stdout=subprocess.PIPE)
    os.chdir("../python")

    while True:
        line = proc.stdout.readline()
        if b'Ready' in line:
            break
    yield proc
    proc.kill()


def test_client(cluster):
    client = Client()
    client.health_check()
