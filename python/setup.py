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

from setuptools import setup

setup(
    name='gubernator',
    version="0.2.0",
    description="Python client for gubernator",
    author="Derrick J. Wippler",
    author_email='thrawn01@gmail.com',
    url='https://github.com/mailgun/gubernator',
    packages=("gubernator",),
    install_requires=("grpcio>=1.54.2,<2", "protobuf>=4.23.1,<5"),
    include_package_data=True,
    license="Apache Software License 2.0",
    python_requires='>=3.8',
    classifiers=[
        'Development Status :: 4 - Beta',
        'Intended Audience :: Developers',
        'License :: OSI Approved :: Apache Software License',
        'Natural Language :: English',
        'Programming Language :: Python :: 3.8',
        'Programming Language :: Python :: 3.9',
        'Programming Language :: Python :: 3.10',
        'Programming Language :: Python :: 3.11',
    ],
)
