#!/usr/bin/env python
# -*- coding: utf-8 -*-

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

try:  # for pip >= 10
    from pip._internal.req import parse_requirements
except ImportError:  # for pip <= 9.0.3
    from pip.req import parse_requirements
from setuptools import setup, find_packages
import platform

with open('version', 'r') as version_file:
    version = version_file.readline().strip()

if platform.python_version_tuple()[0] == '2':
    reqs = parse_requirements('requirements-py2.txt', session='')
else:
    reqs = parse_requirements('requirements-py3.txt', session='')

requirements = [str(r.req) for r in reqs]

setup(
    name='gubernator',
    version='0.1.0',
    description="Python client for gubernator",
    author="Derrick J. Wippler",
    author_email='thrawn01@gmail.com',
    url='https://github.com/mailgun/gubernator',
    package_dir={'': '.'},
    packages=find_packages('.', exclude=['tests']),
    install_requires=requirements,
    license="Apache Software License 2.0",
    python_requires='>=2.7',
    classifiers=[
        'Development Status :: 4 - Beta',
        'Intended Audience :: Developers',
        'License :: OSI Approved :: Apache Software License',
        'Natural Language :: English',
        'Programming Language :: Python :: 2.7',
        'Programming Language :: Python :: 3.5',
        'Programming Language :: Python :: 3.6',
    ],
)
