import pytest
import subprocess
import os

from gubernator.client import Client


#@pytest.fixture(scope='module')
#def create_cluster():
    #pid = subprocess.Popen(["/bin/sh", "-c", 
        #"cd ../golang; go run ./cmd/gubernator-cluster/main.go"]).pid
    #yield pid
    #os.kill(pid)


def test_client():
    pid = subprocess.Popen(["/bin/sh", "-c", "cd ../golang; go run ./cmd/gubernator-cluster/main.go"]).pid
    client = Client()
    client.health_check()
    os.kill(pid)
