@Library('jenkins-library@opensource-release') _
dockerImagePipeline(
  script: this,
  service: 'cmd-forwarder-kernel',
  dockerfile: 'Dockerfile',
  buildContext: '.',
  buildArguments: [PLATFORM:"amd64"]
)
