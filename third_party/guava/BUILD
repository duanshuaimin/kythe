load("@rules_java//java:defs.bzl", "java_library")

package(default_visibility = ["//visibility:public"])

licenses(["notice"])

exports_files(["COPYING"])

java_library(
    name = "guava",
    exports = [
        # Errorprone dep required to solve warnings: "Cannot find annotation method 'value()' in type 'CompatibleWith'"
        "@maven//:com_google_errorprone_error_prone_annotations",
        "@maven//:com_google_guava_guava",
    ],
)
