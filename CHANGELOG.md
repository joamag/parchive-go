# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

*

### Changed

*

### Fixed

*

## [0.1.0] - 2026-08-26

### Added

* Creation, verification and repair of PAR2 recovery sets
* Creation, verification and repair of PAR1 recovery sets
* Recovery of data that moved after bytes were inserted or deleted in a file
* Recovery of slices that ended up inside a different file of the set
* SIMD accelerated encoding on Apple Silicon, ARM servers and x86-64
* Command line compatible with par2cmdline, including its options and exit codes
* Recovery data spread across several volume files, as par2cmdline does
* Single `parchive` command line tool that picks the format from the file extension
* Importable library for both formats, with no third-party dependencies
