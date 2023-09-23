/*
Copyright 2023 José Luis Salvador Rufo <salvador.joseluis@gmail.com>.

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

package rfc1123

import (
	"fmt"
	"hash/crc32"
)

func GenerateSafeLengthName(s string) string {
	const maxHostnameLength = 63
	const crc32Length = 8
	const cut = maxHostnameLength - crc32Length - 1 // -1 == dash character

	if len(s) <= maxHostnameLength {
		return s
	}
	return fmt.Sprintf("%s-%08x", s[0:cut], crc32.ChecksumIEEE([]byte(s)))
}
