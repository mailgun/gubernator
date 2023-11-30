/*
Copyright 2018-2022 Mailgun Technologies Inc

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package gubernator_test

import (
	"testing"

	gubernator "github.com/mailgun/gubernator/v2"
	"github.com/stretchr/testify/assert"
)

func TestMetadataCarrier(t *testing.T) {
	m := make(map[string]string)
	c := gubernator.MetadataCarrier{Map: m}

	c.Set("foo", "bar")
	assert.Equal(t, "bar", c.Get("foo"))
	assert.Equal(t, []string{"foo"}, c.Keys())
	assert.Equal(t, "bar", m["foo"])
}
