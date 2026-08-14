#include <mach-o/dyld.h>
#include <libgen.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <unistd.h>

int main(void) {
    char executable[PATH_MAX];
    uint32_t size = sizeof(executable);
    if (_NSGetExecutablePath(executable, &size) != 0) {
        fputs("packaging spike could not resolve its executable path\n", stderr);
        return 1;
    }

    char first[PATH_MAX];
    char second[PATH_MAX];
    if (snprintf(first, sizeof(first), "%s", executable) >= (int)sizeof(first) ||
        snprintf(second, sizeof(second), "%s", dirname(first)) >= (int)sizeof(second)) {
        return 1;
    }
    char *contents = dirname(second);

    char helper[PATH_MAX];
    char extensions[PATH_MAX];
    if (snprintf(helper, sizeof(helper), "%s/Helpers/quackridge", contents) >= (int)sizeof(helper) ||
        snprintf(extensions, sizeof(extensions), "%s/Resources/Backend/extensions", contents) >= (int)sizeof(extensions)) {
        return 1;
    }

    char state[] = "/private/tmp/QuackRidge Packaging Spike ü.XXXXXX";
    if (mkdtemp(state) == NULL) {
        fputs("packaging spike could not create its temporary state directory\n", stderr);
        return 1;
    }
    char config[PATH_MAX];
    char control[PATH_MAX];
    snprintf(config, sizeof(config), "%s/config.json", state);
    snprintf(control, sizeof(control), "%s/control.sock", state);

    char *const arguments[] = {
        helper, "doctor", "--config", config, "--extensions", extensions,
        "--credential-provider", "environment", "--control", control, "--json", NULL,
    };
    char *const environment[] = {"LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8", NULL};

    pid_t child = fork();
    if (child < 0) {
        rmdir(state);
        return 1;
    }
    if (child == 0) {
        execve(helper, arguments, environment);
        _exit(127);
    }
    int status = 0;
    if (waitpid(child, &status, 0) < 0 || !WIFEXITED(status)) {
        unlink(control);
        unlink(config);
        rmdir(state);
        return 1;
    }
    int result = WEXITSTATUS(status);
    unlink(control);
    unlink(config);
    rmdir(state);
    return result;
}
