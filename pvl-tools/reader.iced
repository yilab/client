#!/usr/bin/env iced
fs = require 'fs'
stringify = require 'json-stable-stringify'
CSON = require 'cson'
{docopt} = require 'docopt'

doc = """
Convert pvl cson to json

Example:
  ./reader.iced pvl.cson --go > ../go/pvl/hardcoded.go

Usage:
  reader.iced <file.cson> [--go]
  reader.iced --help

Options:
  --go    Print the generated contents 'hardcoded.go' to stdout
"""

options = docopt doc

path = options["<file.cson>"]

# read the cson file
pvl_obj = CSON.parseCSONFile path
if 'filename' of pvl_obj
  console.log "error decoding"
  console.log pvl_obj.toString()
  process.exit 1

unless options["--go"]
  pvl_json = stringify pvl_obj
  console.log pvl_json
else
  pvl_json = stringify pvl_obj, space: 2
  gosrc = """
// Copyright 2016 Keybase, Inc. All rights reserved. Use of
// this source code is governed by the included BSD license.

package pvl

var hardcodedPVLString = `
  #{pvl_json}
`

// GetHardcodedPvlString returns the unparsed pvl
func GetHardcodedPvlString() string {
	return hardcodedPVLString
}
  """
  console.log gosrc