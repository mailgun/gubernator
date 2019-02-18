#import pytest
from gubernator.client import Client


def test_client():
    client = Client()
    client.Ping()
