#pragma once

#include <cctype>
#include <cstdio>
#include <cstdint>
#include <stdexcept>
#include <optional>
#include <string>
#include <utility>
#include <vector>

namespace scenjson {

struct Value;

struct Object {
  std::vector<std::pair<std::string, Value>> fields;
};

struct Array {
  std::vector<Value> items;
};

struct Value {
  enum class Kind { Null, Bool, Number, String, Object, Array };
  Kind kind = Kind::Null;
  bool b = false;
  double number = 0;
  std::string str;
  Object obj;
  Array arr;

  static Value makeNull() { return Value{}; }
  static Value makeBool(bool v) {
    Value out;
    out.kind = Kind::Bool;
    out.b = v;
    return out;
  }
  static Value makeNumber(double v) {
    Value out;
    out.kind = Kind::Number;
    out.number = v;
    return out;
  }
  static Value makeString(std::string v) {
    Value out;
    out.kind = Kind::String;
    out.str = std::move(v);
    return out;
  }
  static Value makeObject(Object o) {
    Value out;
    out.kind = Kind::Object;
    out.obj = std::move(o);
    return out;
  }
  static Value makeArray(Array a) {
    Value out;
    out.kind = Kind::Array;
    out.arr = std::move(a);
    return out;
  }
};

class ParseError : public std::runtime_error {
 public:
  using std::runtime_error::runtime_error;
};

inline void skipWs(const std::string& s, size_t& i) {
  while (i < s.size() && std::isspace(static_cast<unsigned char>(s[i]))) {
    ++i;
  }
}

inline Value parseValue(const std::string& s, size_t& i);

inline std::string parseString(const std::string& s, size_t& i) {
  if (i >= s.size() || s[i] != '"') {
    throw ParseError("expected string");
  }
  ++i;
  std::string out;
  while (i < s.size()) {
    const char ch = s[i++];
    if (ch == '"') {
      return out;
    }
    if (ch == '\\' && i < s.size()) {
      out.push_back(s[i++]);
      continue;
    }
    out.push_back(ch);
  }
  throw ParseError("unterminated string");
}

inline double parseNumber(const std::string& s, size_t& i) {
  size_t start = i;
  if (s[i] == '-') {
    ++i;
  }
  while (i < s.size() && (std::isdigit(static_cast<unsigned char>(s[i])) || s[i] == '.')) {
    ++i;
  }
  return std::stod(s.substr(start, i - start));
}

inline Value parseObject(const std::string& s, size_t& i) {
  if (s[i] != '{') {
    throw ParseError("expected object");
  }
  ++i;
  skipWs(s, i);
  Object obj;
  if (i < s.size() && s[i] == '}') {
    ++i;
    return Value::makeObject(std::move(obj));
  }
  while (true) {
    skipWs(s, i);
    std::string key = parseString(s, i);
    skipWs(s, i);
    if (s[i] != ':') {
      throw ParseError("expected colon");
    }
    ++i;
    skipWs(s, i);
    obj.fields.emplace_back(std::move(key), parseValue(s, i));
    skipWs(s, i);
    if (s[i] == '}') {
      ++i;
      break;
    }
    if (s[i] != ',') {
      throw ParseError("expected comma");
    }
    ++i;
  }
  return Value::makeObject(std::move(obj));
}

inline Value parseArray(const std::string& s, size_t& i) {
  if (s[i] != '[') {
    throw ParseError("expected array");
  }
  ++i;
  skipWs(s, i);
  Array arr;
  if (i < s.size() && s[i] == ']') {
    ++i;
    return Value::makeArray(std::move(arr));
  }
  while (true) {
    skipWs(s, i);
    arr.items.push_back(parseValue(s, i));
    skipWs(s, i);
    if (s[i] == ']') {
      ++i;
      break;
    }
    if (s[i] != ',') {
      throw ParseError("expected comma");
    }
    ++i;
  }
  return Value::makeArray(std::move(arr));
}

inline Value parseValue(const std::string& s, size_t& i) {
  skipWs(s, i);
  if (i >= s.size()) {
    throw ParseError("unexpected end");
  }
  const char ch = s[i];
  if (ch == '"') {
    return Value::makeString(parseString(s, i));
  }
  if (ch == '{') {
    return parseObject(s, i);
  }
  if (ch == '[') {
    return parseArray(s, i);
  }
  if (ch == 't') {
    if (s.compare(i, 4, "true") == 0) {
      i += 4;
      return Value::makeBool(true);
    }
  }
  if (ch == 'f') {
    if (s.compare(i, 5, "false") == 0) {
      i += 5;
      return Value::makeBool(false);
    }
  }
  if (ch == 'n') {
    if (s.compare(i, 4, "null") == 0) {
      i += 4;
      return Value::makeNull();
    }
  }
  if (ch == '-' || std::isdigit(static_cast<unsigned char>(ch))) {
    return Value::makeNumber(parseNumber(s, i));
  }
  throw ParseError("bad token");
}

inline Value parse(const std::string& s) {
  size_t i = 0;
  Value v = parseValue(s, i);
  skipWs(s, i);
  if (i != s.size()) {
    throw ParseError("trailing junk");
  }
  return v;
}

inline const Value* objectField(const Object& o, const std::string& key) {
  for (const auto& kv : o.fields) {
    if (kv.first == key) {
      return &kv.second;
    }
  }
  return nullptr;
}

inline std::optional<std::string> getString(const Value& v) {
  if (v.kind != Value::Kind::String) {
    return std::nullopt;
  }
  return v.str;
}

inline std::optional<double> getNumber(const Value& v) {
  if (v.kind != Value::Kind::Number) {
    return std::nullopt;
  }
  return v.number;
}

inline std::optional<bool> getBool(const Value& v) {
  if (v.kind != Value::Kind::Bool) {
    return std::nullopt;
  }
  return v.b;
}

inline std::string jsonEscape(const std::string& in) {
  std::string out;
  for (char ch : in) {
    switch (ch) {
      case '"':
        out += "\\\"";
        break;
      case '\\':
        out += "\\\\";
        break;
      case '\n':
        out += "\\n";
        break;
      case '\r':
        out += "\\r";
        break;
      default:
        out.push_back(ch);
        break;
    }
  }
  return out;
}

inline std::string stringify(const Value& v, int indent = 0, int level = 0);

inline std::string stringifyObject(const Object& o, int indent, int level) {
  std::string pad(level * indent, ' ');
  std::string padIn((level + 1) * indent, ' ');
  std::string out = "{\n";
  for (size_t i = 0; i < o.fields.size(); ++i) {
    out += padIn + "\"" + jsonEscape(o.fields[i].first) + "\": " +
           stringify(o.fields[i].second, indent, level + 1);
    if (i + 1 < o.fields.size()) {
      out += ',';
    }
    out += '\n';
  }
  out += pad + '}';
  return out;
}

inline std::string stringifyArray(const Array& a, int indent, int level) {
  if (a.items.empty()) {
    return "[]";
  }
  std::string pad(level * indent, ' ');
  std::string padIn((level + 1) * indent, ' ');
  std::string out = "[\n";
  for (size_t i = 0; i < a.items.size(); ++i) {
    out += padIn + stringify(a.items[i], indent, level + 1);
    if (i + 1 < a.items.size()) {
      out += ',';
    }
    out += '\n';
  }
  out += pad + ']';
  return out;
}

inline std::string stringify(const Value& v, int indent, int level) {
  switch (v.kind) {
    case Value::Kind::Null:
      return "null";
    case Value::Kind::Bool:
      return v.b ? "true" : "false";
    case Value::Kind::Number: {
      const auto n = static_cast<int64_t>(v.number);
      if (static_cast<double>(n) == v.number) {
        return std::to_string(n);
      }
      char buf[64];
      std::snprintf(buf, sizeof(buf), "%.17g", v.number);
      return buf;
    }
    case Value::Kind::String:
      return "\"" + jsonEscape(v.str) + "\"";
    case Value::Kind::Object:
      return stringifyObject(v.obj, indent, level);
    case Value::Kind::Array:
      return stringifyArray(v.arr, indent, level);
  }
  return "null";
}

}  // namespace scenjson
