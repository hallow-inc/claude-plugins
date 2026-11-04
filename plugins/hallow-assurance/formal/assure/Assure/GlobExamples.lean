import Assure.Glob

namespace Assure

#guard globMatch "**" ""
#guard globMatch "**" "a"
#guard globMatch "**" "a/b/c.go"
#guard globMatch "**/a.go" "a.go"
#guard globMatch "**/a.go" "b/b/a.go"
#guard !globMatch "**/a.go" "b/ba.go"
#guard globMatch "a/**/b.go" "a/b.go"
#guard globMatch "a/**/b.go" "a/a/b/b.go"
#guard !globMatch "a/**/b.go" "b/a/b.go"
#guard globMatch "a/**" "a"
#guard globMatch "a/**" "a/b/a.go"
#guard !globMatch "a/**" "b/a.go"
#guard globMatch "*.go" "ab.go"
#guard !globMatch "*.go" "a/b.go"
#guard globMatch "a/*.go" "a/b.go"
#guard !globMatch "a/*" "a/b/c"
#guard globMatch "a*b" "ab"
#guard globMatch "a*b" "aab/"  == false
#guard globMatch "?.go" "a.go"
#guard !globMatch "?.go" "ab.go"
#guard !globMatch "?" ""
#guard globMatch "a**b" "aab"
#guard !globMatch "a**b" "a/b"
#guard !globMatch "a.go" "a.go/"

end Assure
