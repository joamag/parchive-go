# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

* Recovery of data that moved after bytes were inserted or deleted in a file
* Recovery of slices that ended up inside a different file of the set
* SIMD accelerated encoding on Apple Silicon, ARM servers and x86-64

### Changed

* Creating a recovery set is around eleven times faster and now matches par2cmdline-turbo
* Input files are read once instead of twice when creating a recovery set
* Recovery data is computed across all available processor cores

### Fixed

*

## [0.1.0] - 2026-08-26

### Added

* Creation, verification and repair of PAR2 recovery sets
* Creation, verification and repair of PAR1 recovery sets
* Single `parchive` command line tool that picks the format from the file extension
* Importable library for both formats, with no third-party dependencies
