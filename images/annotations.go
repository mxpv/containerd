/*
   Copyright The containerd Authors.

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

package images

const (
	// AnnotationImageName is an annotation on a Descriptor in an index.json
	// containing the `Name` value as used by an `Image` struct
	AnnotationImageName = "io.containerd.image.name"

	// AnnotationImageNamePrefix is used the same way as AnnotationImageName
	// but may be used to refer to additional names in the annotation map
	// using user-defined suffixes (i.e. "extra.1")
	AnnotationImageNamePrefix = AnnotationImageName + "."
)
