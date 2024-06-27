alias kubectl=/usr/local/bin/kubectl
cd ~/rh/src/kube-compare/
PS1='$ '
clear

echo_read_exec(){
    cmd=$@
    echo -e '$ '$cmd'\c'
    read
    $cmd
}

http_demo()(
    echo_read_exec curl -l https://raw.githubusercontent.com/nocturnalastro/kube-compare/demo-255/demo/http/reference/metadata.yaml

    read

    echo_read_exec kubectl cluster-compare -r https://raw.githubusercontent.com/nocturnalastro/kube-compare/demo-255/demo/http/reference/ -R -f demo/http/resources-no-diff/

    read

    echo_read_exec kubectl cluster-compare -r https://raw.githubusercontent.com/nocturnalastro/kube-compare/demo-255/demo/http/reference/ -R -f demo/http/resources-diff/
)

merge_demo() {
    echo_read_exec cat demo/merge/reference-no-merge/metadata.yaml
    read
    echo_read_exec kubectl cluster-compare -r demo/merge/reference-no-merge -R -f demo/merge/resources/

    read

    echo_read_exec cat demo/merge/reference-merge/metadata.yaml
    read
    echo_read_exec kubectl cluster-compare -r demo/merge/reference-merge -R -f demo/merge/resources/
}
