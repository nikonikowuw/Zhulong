#include "json.hpp"
#include <cassert>
#include <iostream>
#include <string>

int main() {
    std::string value = "quote \" slash \\ control \b\f\n\r\t";
    value.push_back('\x01');
    value += " UTF-8: \xE4\xB8\xAD";

    const std::string expected =
        "\"quote \\\" slash \\\\ control \\b\\f\\n\\r\\t\\u0001 UTF-8: \xE4\xB8\xAD\"";
    const std::string encoded = zhulong::runner::json_quote(value);
    assert(encoded == expected);
    std::cout << encoded << '\n';
    return 0;
}
